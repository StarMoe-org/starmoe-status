package prober

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
	"github.com/StarMoe-org/starmoe-status/server/internal/alert"
	"github.com/StarMoe-org/starmoe-status/server/internal/config"
	"github.com/StarMoe-org/starmoe-status/server/internal/store"
)

// ProbeResult is the outcome of a single active check.
type ProbeResult struct {
	State       string
	StatusCode  *int
	LatencyMs   int64
	TLSDaysLeft *int
	Message     string
}

// Prober runs concurrent active HTTP/HTTPS/TLS/TCP checks on a schedule.
type Prober struct {
	store       *store.Store
	alerts      *alert.Engine
	monitors    []config.MonitorConfig
	interval    time.Duration
	concurrency int
	logger      *slog.Logger
	transport   *http.Transport
}

// New constructs a Prober.
func New(st *store.Store, al *alert.Engine, cfg *config.Config, logger *slog.Logger) *Prober {
	if logger == nil {
		logger = slog.Default()
	}
	interval := time.Duration(cfg.ProbeIntervalSec) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}
	var active []config.MonitorConfig
	for _, m := range cfg.Monitors {
		if m.Kind == protocol.KindHTTP || m.Kind == protocol.KindTLS || m.Kind == protocol.KindTCP {
			active = append(active, m)
		}
	}
	return &Prober{
		store:       st,
		alerts:      al,
		monitors:    active,
		interval:    interval,
		concurrency: 8,
		logger:      logger,
		transport: &http.Transport{
			MaxIdleConns:        32,
			IdleConnTimeout:     30 * time.Second,
			TLSHandshakeTimeout: 8 * time.Second,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
}

// Run starts the probe loop, running one round immediately and then every interval.
func (p *Prober) Run(ctx context.Context) {
	p.RunOnce(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.RunOnce(ctx)
		}
	}
}

// RunOnce executes one concurrent round of all active monitors.
func (p *Prober) RunOnce(ctx context.Context) {
	if len(p.monitors) == 0 {
		return
	}
	sem := make(chan struct{}, p.concurrency)
	var wg sync.WaitGroup
	for _, m := range p.monitors {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(mon config.MonitorConfig) {
			defer wg.Done()
			defer func() { <-sem }()
			res := p.ProbeMonitor(ctx, mon)
			if err := p.ApplyResult(ctx, mon, res, time.Now().Unix()); err != nil {
				p.logger.Error("apply probe result failed", "monitor", mon.ID, "err", err)
			}
		}(m)
	}
	wg.Wait()
}

// ProbeMonitor executes the active probe for a single monitor config.
func (p *Prober) ProbeMonitor(ctx context.Context, m config.MonitorConfig) ProbeResult {
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch m.Kind {
	case protocol.KindTCP:
		return p.probeTCP(probeCtx, m.TCPAddr, timeout)
	default:
		return p.probeHTTP(probeCtx, m, timeout)
	}
}

func (p *Prober) probeTCP(ctx context.Context, addr string, timeout time.Duration) ProbeResult {
	start := time.Now()
	d := &net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return ProbeResult{
			State:     protocol.StateDown,
			LatencyMs: lat,
			Message:   fmt.Sprintf("tcp dial failed: %v", err),
		}
	}
	_ = conn.Close()
	return ProbeResult{
		State:     protocol.StateUp,
		LatencyMs: lat,
		Message:   "ok",
	}
}

func (p *Prober) probeHTTP(ctx context.Context, m config.MonitorConfig, timeout time.Duration) ProbeResult {
	client := &http.Client{
		Transport: p.transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// If 301/302 is explicitly expected, stop following redirects so we observe it directly.
			if slices.Contains(m.ExpectedStatus, 301) || slices.Contains(m.ExpectedStatus, 302) {
				return http.ErrUseLastResponse
			}
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	res := p.doSingleHTTP(ctx, client, m.Method, m.URL, m.ExpectedStatus, m.JSONAssert)
	if res.State == protocol.StateDown {
		return res
	}

	// Check extra URLs if configured; failure in a secondary URL degrades the monitor.
	for _, ex := range m.ExtraURLs {
		exRes := p.doSingleHTTP(ctx, client, ex.Method, ex.URL, ex.ExpectedStatus, "")
		if exRes.State != protocol.StateUp {
			if res.State == protocol.StateUp {
				res.State = protocol.StateDegraded
			}
			res.Message = fmt.Sprintf("secondary %s: %s", ex.URL, exRes.Message)
		}
		if exRes.TLSDaysLeft != nil {
			if res.TLSDaysLeft == nil || *exRes.TLSDaysLeft < *res.TLSDaysLeft {
				d := *exRes.TLSDaysLeft
				res.TLSDaysLeft = &d
			}
		}
	}
	return res
}

func (p *Prober) doSingleHTTP(ctx context.Context, client *http.Client, method, targetURL string, expectedStatus []int, jsonAssert string) ProbeResult {
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, nil)
	if err != nil {
		return ProbeResult{State: protocol.StateDown, Message: fmt.Sprintf("invalid url: %v", err)}
	}
	req.Header.Set("User-Agent", "StarMoe-Status-Prober/1.0")

	start := time.Now()
	resp, err := client.Do(req)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return ProbeResult{
			State:     protocol.StateDown,
			LatencyMs: lat,
			Message:   err.Error(),
		}
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	res := ProbeResult{
		State:      protocol.StateUp,
		StatusCode: &code,
		LatencyMs:  lat,
		Message:    fmt.Sprintf("HTTP %d", code),
	}

	// Check TLS certificate expiry if HTTPS.
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		notAfter := resp.TLS.PeerCertificates[0].NotAfter
		days := int(time.Until(notAfter).Hours() / 24)
		res.TLSDaysLeft = &days
		if days < 3 {
			res.State = protocol.StateDown
			res.Message = fmt.Sprintf("TLS cert expires in %d days", days)
		} else if days < 14 {
			res.State = protocol.StateDegraded
			res.Message = fmt.Sprintf("TLS cert expires in %d days", days)
		}
	}

	// Validate HTTP status code.
	if len(expectedStatus) > 0 && !slices.Contains(expectedStatus, code) {
		res.State = protocol.StateDown
		res.Message = fmt.Sprintf("unexpected HTTP %d", code)
		return res
	}

	// Evaluate optional JSON assertion.
	if jsonAssert != "" && method != http.MethodHead {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		if err != nil {
			res.State = protocol.StateDown
			res.Message = fmt.Sprintf("read body failed: %v", err)
			return res
		}
		ok, reason := EvaluateJSONAssert(body, jsonAssert)
		if !ok {
			res.State = protocol.StateDown
			res.Message = reason
			return res
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	}

	return res
}

// EvaluateJSONAssert evaluates expressions like "redis==true" or "ok&&ready" against a JSON object.
func EvaluateJSONAssert(body []byte, expr string) (bool, string) {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return false, "invalid json body"
	}

	clauses := strings.Split(expr, "&&")
	for _, rawClause := range clauses {
		clause := strings.TrimSpace(rawClause)
		if clause == "" {
			continue
		}
		if k, v, found := strings.Cut(clause, "!="); found {
			val, exists := lookupJSONPath(obj, strings.TrimSpace(k))
			if !exists {
				return false, fmt.Sprintf("json key %q missing", strings.TrimSpace(k))
			}
			if stringifyValue(val) == strings.TrimSpace(v) {
				return false, fmt.Sprintf("json assertion failed: %s", clause)
			}
			continue
		}
		if k, v, found := strings.Cut(clause, "=="); found {
			val, exists := lookupJSONPath(obj, strings.TrimSpace(k))
			if !exists {
				return false, fmt.Sprintf("json key %q missing", strings.TrimSpace(k))
			}
			if stringifyValue(val) != strings.TrimSpace(v) {
				return false, fmt.Sprintf("json assertion failed: %s (got %s)", clause, stringifyValue(val))
			}
			continue
		}
		// Bare truthy key
		val, exists := lookupJSONPath(obj, clause)
		if !exists || !isTruthy(val) {
			return false, fmt.Sprintf("json key %q not truthy", clause)
		}
	}
	return true, ""
}

func lookupJSONPath(obj map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = obj
	for _, part := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, exists := m[part]
		if !exists {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

func stringifyValue(v any) string {
	switch val := v.(type) {
	case bool:
		if val {
			return "true"
		}
		return "false"
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}

func isTruthy(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		l := strings.ToLower(strings.TrimSpace(val))
		return l != "" && l != "false" && l != "0" && l != "error" && l != "fail"
	case float64:
		return val != 0
	default:
		return v != nil
	}
}

// ApplyResult records the sample, runs consecutive failure/recovery debounce,
// updates monitor state, and triggers alert transitions when state changes.
func (p *Prober) ApplyResult(ctx context.Context, m config.MonitorConfig, res ProbeResult, checkedAt int64) error {
	lat := res.LatencyMs
	if err := p.store.RecordCheckSample(ctx, store.CheckSample{
		MonitorID:   m.ID,
		State:       res.State,
		StatusCode:  res.StatusCode,
		LatencyMs:   &lat,
		TLSDaysLeft: res.TLSDaysLeft,
		Message:     res.Message,
		CheckedAt:   checkedAt,
	}); err != nil {
		return err
	}

	cur, err := p.store.GetMonitor(ctx, m.ID)
	if err != nil {
		return err
	}

	failThreshold := m.FailThreshold
	if failThreshold <= 0 {
		failThreshold = 3
	}
	okThreshold := m.OKThreshold
	if okThreshold <= 0 {
		okThreshold = 2
	}

	prevState := cur.CurrentState
	nextState := prevState
	fails := cur.ConsecutiveFails
	oks := cur.ConsecutiveOKs

	switch res.State {
	case protocol.StateDown:
		fails++
		oks = 0
		if fails >= failThreshold {
			nextState = protocol.StateDown
		}
	case protocol.StateDegraded:
		fails = 0
		if prevState == protocol.StateDown {
			oks++
			if oks >= okThreshold {
				nextState = protocol.StateDegraded
				oks = 0
			}
		} else {
			oks = 0
			nextState = protocol.StateDegraded
		}
	case protocol.StateUp:
		fails = 0
		oks++
		if prevState == protocol.StateUnknown || oks >= okThreshold {
			nextState = protocol.StateUp
		}
	}

	metaJSON := mergeTLSMeta(cur.MetaJSON, res.TLSDaysLeft)
	if err := p.store.UpdateMonitorState(ctx, m.ID, nextState, fails, oks, &lat, res.Message, metaJSON, checkedAt); err != nil {
		return err
	}

	if nextState != prevState {
		// Suppress noise on initial unknown -> up boot transition.
		if prevState == protocol.StateUnknown && nextState == protocol.StateUp {
			return nil
		}
		if p.alerts != nil {
			summary := fmt.Sprintf("%s: %s -> %s (%s)", cur.Name, prevState, nextState, res.Message)
			_, err = p.alerts.RecordTransition(ctx, cur, prevState, nextState, summary, nil)
			return err
		}
	}
	return nil
}

func mergeTLSMeta(existing string, tlsDays *int) string {
	if tlsDays == nil {
		return ""
	}
	m := make(map[string]any)
	if existing != "" {
		_ = json.Unmarshal([]byte(existing), &m)
	}
	m["tls_days_left"] = *tlsDays
	b, _ := json.Marshal(m)
	return string(b)
}
