package deep

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
)

// PodAliveChecker is satisfied by k8s.Client.
type PodAliveChecker interface {
	CheckPodsAlive(ctx context.Context, namespace, namePrefix string) (readyPods int, totalPods int, err error)
}

// CheckConfig defines a single per-cluster deep health check loaded from DEEP_CHECKS_JSON.
type CheckConfig struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Kind       string `json:"kind"` // sekai_health | masterdata_sync | meta_builder | story_search | http | tcp | pod_alive
	URL        string `json:"url,omitempty"`
	Addr       string `json:"addr,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	PodPrefix  string `json:"pod_prefix,omitempty"`
	MinReady   int    `json:"min_ready,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

// Checker executes per-cluster deep health checks concurrently.
type Checker struct {
	httpClient *http.Client
	pods       PodAliveChecker
	now        func() time.Time
}

// NewChecker creates a deep health checker.
func NewChecker(httpClient *http.Client, pods PodAliveChecker) *Checker {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 8 * time.Second}
	}
	return &Checker{
		httpClient: httpClient,
		pods:       pods,
		now:        time.Now,
	}
}

// RunAll executes all configured deep checks concurrently and preserves input ordering.
func (c *Checker) RunAll(ctx context.Context, configs []CheckConfig) []protocol.DeepCheckResult {
	if len(configs) == 0 {
		return nil
	}
	results := make([]protocol.DeepCheckResult, len(configs))
	var wg sync.WaitGroup
	for i, cfg := range configs {
		wg.Add(1)
		go func(idx int, cc CheckConfig) {
			defer wg.Done()
			timeout := time.Duration(cc.TimeoutSec) * time.Second
			if timeout <= 0 {
				timeout = 8 * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			results[idx] = c.runOne(cctx, cc)
		}(i, cfg)
	}
	wg.Wait()
	return results
}

func (c *Checker) runOne(ctx context.Context, cfg CheckConfig) protocol.DeepCheckResult {
	start := time.Now()
	res := protocol.DeepCheckResult{
		MonitorID: cfg.ID,
		Name:      cfg.Name,
	}

	switch cfg.Kind {
	case "sekai_health":
		c.checkSekaiHealth(ctx, cfg.URL, &res)
	case "masterdata_sync":
		c.checkMasterdataSync(ctx, cfg.URL, &res)
	case "meta_builder":
		c.checkMetaBuilder(ctx, cfg.URL, &res)
	case "story_search":
		c.checkStorySearch(ctx, cfg.URL, &res)
	case "http":
		c.checkHTTP(ctx, cfg.URL, &res)
	case "tcp":
		c.checkTCP(ctx, cfg.Addr, &res)
	case "pod_alive":
		c.checkPodAlive(ctx, cfg, &res)
	default:
		res.State = protocol.StateUnknown
		res.Message = fmt.Sprintf("unsupported check kind %q", cfg.Kind)
	}

	res.LatencyMs = time.Since(start).Milliseconds()
	return res
}

func (c *Checker) fetchBody(ctx context.Context, targetURL string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

// checkSekaiHealth evaluates US moe-sekai-api-derm /health for jp/cn/tw/kr/en per docs/design.md §5.2.
func (c *Checker) checkSekaiHealth(ctx context.Context, targetURL string, res *protocol.DeepCheckResult) {
	code, body, err := c.fetchBody(ctx, targetURL)
	if err != nil {
		res.State = protocol.StateDown
		res.Message = err.Error()
		return
	}
	if code < 200 || code >= 300 {
		res.State = protocol.StateDown
		res.Message = fmt.Sprintf("http %d", code)
		return
	}

	type regionInfo struct {
		ActiveSessions *int    `json:"active_sessions"`
		LastLoginError *string `json:"last_login_error"`
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		res.State = protocol.StateDegraded
		res.Message = "invalid json payload"
		return
	}

	// Support {"servers": {...}}, {"regions": {...}}, or top-level {"jp": {...}}.
	regionsRaw := raw
	for _, wrapper := range []string{"servers", "regions"} {
		if inner, ok := raw[wrapper]; ok {
			var m map[string]json.RawMessage
			if json.Unmarshal(inner, &m) == nil && len(m) > 0 {
				regionsRaw = m
				break
			}
		}
	}

	order := []struct {
		key   string
		label string
	}{
		{"jp", "JP 日服"},
		{"cn", "CN 国服"},
		{"tw", "TW 台服"},
		{"kr", "KR 韩服"},
		{"en", "EN 国际服"},
	}

	worst := protocol.StateUp
	var degradedRegions, downRegions []string

	for _, item := range order {
		val, exists := regionsRaw[item.key]
		if !exists {
			continue
		}
		var info regionInfo
		if err := json.Unmarshal(val, &info); err != nil {
			continue
		}

		sub := protocol.SubItemStatus{
			Key:            item.key,
			Label:          item.label,
			State:          protocol.StateUp,
			ActiveSessions: info.ActiveSessions,
		}
		if info.LastLoginError != nil {
			sub.LastError = strings.TrimSpace(*info.LastLoginError)
		}

		sessions := -1
		if info.ActiveSessions != nil {
			sessions = *info.ActiveSessions
		}

		switch {
		case sessions == 0:
			sub.State = protocol.StateDown
			sub.Message = "0 active sessions"
			downRegions = append(downRegions, strings.ToUpper(item.key))
			worst = protocol.StateDown
		case sessions == 1 || sub.LastError != "":
			sub.State = protocol.StateDegraded
			if sub.LastError != "" {
				sub.Message = sub.LastError
			} else {
				sub.Message = "1 active session"
			}
			degradedRegions = append(degradedRegions, strings.ToUpper(item.key))
			if worst != protocol.StateDown {
				worst = protocol.StateDegraded
			}
		default:
			sub.State = protocol.StateUp
		}
		res.SubItems = append(res.SubItems, sub)
	}

	if len(res.SubItems) == 0 {
		res.State = protocol.StateDegraded
		res.Message = "no region entries in /health"
		return
	}

	res.State = worst
	switch {
	case len(downRegions) > 0:
		res.Message = fmt.Sprintf("regions down: %s", strings.Join(downRegions, ", "))
	case len(degradedRegions) > 0:
		res.Message = fmt.Sprintf("regions degraded: %s", strings.Join(degradedRegions, ", "))
	default:
		res.Message = "all regions healthy"
	}
}

// checkMasterdataSync evaluates /healthz for moesekai-masterdata-sync per docs/design.md §5.2.
func (c *Checker) checkMasterdataSync(ctx context.Context, targetURL string, res *protocol.DeepCheckResult) {
	code, body, err := c.fetchBody(ctx, targetURL)
	if err != nil {
		res.State = protocol.StateDown
		res.Message = err.Error()
		return
	}
	if code < 200 || code >= 300 {
		res.State = protocol.StateDown
		res.Message = fmt.Sprintf("http %d", code)
		return
	}

	var payload struct {
		ConsecutiveFailures int `json:"consecutiveFailures"`
		LastSuccess         any `json:"lastSuccess"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		res.State = protocol.StateDegraded
		res.Message = "invalid json"
		return
	}

	var age time.Duration
	hasLastSuccess := false
	if payload.LastSuccess != nil {
		switch v := payload.LastSuccess.(type) {
		case string:
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				age = c.now().Sub(t)
				hasLastSuccess = true
			}
		case float64:
			if v > 1e12 {
				age = c.now().Sub(time.UnixMilli(int64(v)))
			} else if v > 0 {
				age = c.now().Sub(time.Unix(int64(v), 0))
			}
			hasLastSuccess = v > 0
		}
	}

	res.Details = map[string]any{
		"consecutive_failures": payload.ConsecutiveFailures,
	}
	if hasLastSuccess {
		res.Details["last_success_age_sec"] = int(age.Seconds())
	}

	switch {
	case payload.ConsecutiveFailures >= 10 || (hasLastSuccess && age > 60*time.Minute):
		res.State = protocol.StateDown
		res.Message = fmt.Sprintf("sync stale (failures=%d, age=%dm)", payload.ConsecutiveFailures, int(age.Minutes()))
	case payload.ConsecutiveFailures >= 3 || (hasLastSuccess && age > 15*time.Minute):
		res.State = protocol.StateDegraded
		res.Message = fmt.Sprintf("sync lagging (failures=%d, age=%dm)", payload.ConsecutiveFailures, int(age.Minutes()))
	default:
		res.State = protocol.StateUp
		res.Message = "sync healthy"
	}
}

// checkMetaBuilder evaluates pjsk-meta-builder /healthz per docs/design.md §5.2.
func (c *Checker) checkMetaBuilder(ctx context.Context, targetURL string, res *protocol.DeepCheckResult) {
	code, body, err := c.fetchBody(ctx, targetURL)
	if err != nil {
		res.State = protocol.StateDown
		res.Message = err.Error()
		return
	}
	if code < 200 || code >= 300 {
		res.State = protocol.StateDown
		res.Message = fmt.Sprintf("http %d", code)
		return
	}

	var payload struct {
		Status   string `json:"status"`
		SelfTest struct {
			Failed json.RawMessage `json:"failed"`
		} `json:"selfTest"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		res.State = protocol.StateDegraded
		res.Message = "invalid json"
		return
	}

	failedCount := 0
	if len(payload.SelfTest.Failed) > 0 && string(payload.SelfTest.Failed) != "null" {
		var arr []any
		var n int
		var m map[string]any
		if json.Unmarshal(payload.SelfTest.Failed, &arr) == nil {
			failedCount = len(arr)
		} else if json.Unmarshal(payload.SelfTest.Failed, &n) == nil {
			failedCount = n
		} else if json.Unmarshal(payload.SelfTest.Failed, &m) == nil {
			failedCount = len(m)
		}
	}

	res.Details = map[string]any{
		"status":           payload.Status,
		"self_test_failed": failedCount,
	}

	if strings.ToLower(payload.Status) != "ok" || failedCount > 0 {
		res.State = protocol.StateDegraded
		res.Message = fmt.Sprintf("status=%s failed=%d", payload.Status, failedCount)
		return
	}
	res.State = protocol.StateUp
	res.Message = "ok"
}

// checkStorySearch evaluates sekai-story-search /healthz per docs/design.md §5.2.
func (c *Checker) checkStorySearch(ctx context.Context, targetURL string, res *protocol.DeepCheckResult) {
	code, body, err := c.fetchBody(ctx, targetURL)
	if err != nil {
		res.State = protocol.StateDown
		res.Message = err.Error()
		return
	}
	if code < 200 || code >= 300 {
		res.State = protocol.StateDown
		res.Message = fmt.Sprintf("http %d", code)
		return
	}

	var payload struct {
		OK      bool `json:"ok"`
		Ready   bool `json:"ready"`
		Regions map[string]struct {
			Loaded bool `json:"loaded"`
		} `json:"regions"`
		Servers map[string]struct {
			Loaded bool `json:"loaded"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		res.State = protocol.StateDegraded
		res.Message = "invalid json"
		return
	}

	if !payload.OK || !payload.Ready {
		res.State = protocol.StateDown
		res.Message = fmt.Sprintf("ok=%v ready=%v", payload.OK, payload.Ready)
		return
	}

	unloaded := []string{}
	for k, v := range payload.Regions {
		if !v.Loaded {
			unloaded = append(unloaded, k)
		}
	}
	for k, v := range payload.Servers {
		if !v.Loaded {
			unloaded = append(unloaded, k)
		}
	}
	if len(unloaded) > 0 {
		res.State = protocol.StateDegraded
		res.Message = fmt.Sprintf("unloaded regions: %s", strings.Join(unloaded, ", "))
		return
	}
	res.State = protocol.StateUp
	res.Message = "ok and ready"
}

func (c *Checker) checkHTTP(ctx context.Context, targetURL string, res *protocol.DeepCheckResult) {
	code, _, err := c.fetchBody(ctx, targetURL)
	if err != nil {
		res.State = protocol.StateDown
		res.Message = err.Error()
		return
	}
	if code >= 200 && code < 300 {
		res.State = protocol.StateUp
		res.Message = fmt.Sprintf("http %d", code)
		return
	}
	res.State = protocol.StateDown
	res.Message = fmt.Sprintf("http %d", code)
}

func (c *Checker) checkTCP(ctx context.Context, addr string, res *protocol.DeepCheckResult) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		res.State = protocol.StateDown
		res.Message = err.Error()
		return
	}
	_ = conn.Close()
	res.State = protocol.StateUp
	res.Message = "tcp ok"
}

func (c *Checker) checkPodAlive(ctx context.Context, cfg CheckConfig, res *protocol.DeepCheckResult) {
	if c.pods == nil {
		res.State = protocol.StateUnknown
		res.Message = "k8s client unavailable"
		return
	}
	ready, total, err := c.pods.CheckPodsAlive(ctx, cfg.Namespace, cfg.PodPrefix)
	if err != nil {
		res.State = protocol.StateDown
		res.Message = err.Error()
		return
	}
	minReady := cfg.MinReady
	if minReady <= 0 {
		minReady = 1
	}
	res.Details = map[string]any{
		"ready_pods": ready,
		"total_pods": total,
	}
	switch {
	case ready == 0:
		res.State = protocol.StateDown
		res.Message = fmt.Sprintf("0/%d pods ready", total)
	case ready < minReady:
		res.State = protocol.StateDegraded
		res.Message = fmt.Sprintf("%d/%d pods ready (want %d)", ready, total, minReady)
	default:
		res.State = protocol.StateUp
		res.Message = fmt.Sprintf("%d/%d pods ready", ready, total)
	}
}
