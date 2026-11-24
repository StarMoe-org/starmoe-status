package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
	"github.com/StarMoe-org/starmoe-status/server/internal/config"
	"github.com/StarMoe-org/starmoe-status/server/internal/store"
)

// Engine manages state transition recording, topology/maintenance suppression,
// Bot SSE broadcasting, and optional email fallback/P0 alerting.
type Engine struct {
	store         *store.Store
	email         *EmailNotifier
	emailPolicy   string
	publicBaseURL string
	logger        *slog.Logger

	mu             sync.RWMutex
	clusterDown    map[string]bool
	subscribers    map[uint64]chan protocol.AlertEvent
	nextSubID      uint64
	botLastSeenAt  int64
	botOfflineFrom int64
}

// New creates a new alert Engine.
func New(st *store.Store, cfg *config.Config, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		store:         st,
		email:         NewEmailNotifier(cfg),
		emailPolicy:   cfg.EmailPolicy,
		publicBaseURL: cfg.PublicBaseURL,
		logger:        logger,
		clusterDown:   make(map[string]bool),
		subscribers:   make(map[uint64]chan protocol.AlertEvent),
	}
}

// SetClusterDown updates the in-memory topology suppression flag for a cluster.
func (e *Engine) SetClusterDown(cluster string, down bool) {
	if cluster == "" {
		return
	}
	e.mu.Lock()
	if down {
		e.clusterDown[cluster] = true
	} else {
		delete(e.clusterDown, cluster)
	}
	e.mu.Unlock()
}

// IsClusterDown reports whether a cluster is currently marked down.
func (e *Engine) IsClusterDown(cluster string) bool {
	if cluster == "" {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.clusterDown[cluster]
}

// Subscribe registers a Bot SSE client and returns its subscription ID and event channel.
func (e *Engine) Subscribe() (uint64, <-chan protocol.AlertEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextSubID++
	id := e.nextSubID
	ch := make(chan protocol.AlertEvent, 64)
	e.subscribers[id] = ch
	e.botLastSeenAt = time.Now().Unix()
	e.botOfflineFrom = 0
	return id, ch
}

// Unsubscribe removes a Bot SSE client.
func (e *Engine) Unsubscribe(id uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ch, ok := e.subscribers[id]; ok {
		delete(e.subscribers, id)
		close(ch)
	}
	now := time.Now().Unix()
	e.botLastSeenAt = now
	if len(e.subscribers) == 0 {
		e.botOfflineFrom = now
	}
}

// TouchBotSeen updates the last-seen timestamp when a bot polls /api/bot/events or /api/bot/summary.
func (e *Engine) TouchBotSeen() {
	e.mu.Lock()
	e.botLastSeenAt = time.Now().Unix()
	e.mu.Unlock()
}

// BotStatus returns whether at least one Bot SSE client is currently connected.
func (e *Engine) BotStatus() (online bool, clients int, lastSeenAt int64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	clients = len(e.subscribers)
	online = clients > 0
	lastSeenAt = e.botLastSeenAt
	return
}

// Broadcast sends an AlertEvent to all connected Bot SSE clients without blocking.
func (e *Engine) Broadcast(ev protocol.AlertEvent) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, ch := range e.subscribers {
		select {
		case ch <- ev:
		default:
			// Slow consumer; will recover via cursor replay on reconnect.
		}
	}
}

// RecordTransition persists a state transition, applies topology & maintenance suppression,
// broadcasts to Bot SSE clients, and dispatches email according to EMAIL_POLICY.
func (e *Engine) RecordTransition(ctx context.Context, mon *store.MonitorRow, prevState, nextState, summary string, detail json.RawMessage) (*protocol.AlertEvent, error) {
	now := time.Now().Unix()

	// Update topology map when an infra node monitor itself transitions.
	if mon.Kind == protocol.KindAgentNode && mon.Cluster != "" {
		e.SetClusterDown(mon.Cluster, nextState == protocol.StateDown)
	}

	var suppressedBy string
	// 1. Topology suppression: child service transitions are suppressed while their parent cluster node is down.
	if mon.Kind != protocol.KindAgentNode && mon.Cluster != "" && e.IsClusterDown(mon.Cluster) {
		suppressedBy = "node-down:" + mon.Cluster
	}

	// 2. Maintenance window auto-silence.
	if suppressedBy == "" {
		if incID, inMaint, err := e.store.ActiveMaintenanceForMonitor(ctx, mon.ID, now); err == nil && inMaint {
			suppressedBy = "maintenance:" + incID
		}
	}

	ev := protocol.AlertEvent{
		MonitorID:    mon.ID,
		MonitorName:  mon.Name,
		Cluster:      mon.Cluster,
		Severity:     mon.Severity,
		PrevState:    prevState,
		NextState:    nextState,
		SuppressedBy: suppressedBy,
		Summary:      summary,
		Detail:       detail,
		CreatedAt:    now,
	}

	id, err := e.store.InsertStateEvent(ctx, ev)
	if err != nil {
		return nil, err
	}
	ev.ID = id

	if suppressedBy != "" {
		e.logger.Info("alert suppressed", "monitor", mon.ID, "prev", prevState, "next", nextState, "suppressed_by", suppressedBy)
		return &ev, nil
	}

	e.Broadcast(ev)
	e.maybeSendEmail(ctx, ev)
	return &ev, nil
}

func (e *Engine) maybeSendEmail(ctx context.Context, ev protocol.AlertEvent) {
	if e.emailPolicy == "off" || !e.email.Enabled() {
		return
	}

	shouldSend := false
	switch e.emailPolicy {
	case "p0_always":
		if ev.Severity == protocol.SeverityP0 {
			shouldSend = true
		}
	case "fallback":
		e.mu.RLock()
		online := len(e.subscribers) > 0
		offlineFrom := e.botOfflineFrom
		lastSeen := e.botLastSeenAt
		e.mu.RUnlock()

		// Send fallback email if no bot is connected (either never connected, or offline > 2 minutes).
		if !online {
			if lastSeen == 0 || (offlineFrom > 0 && time.Now().Unix()-offlineFrom >= 120) {
				shouldSend = true
			}
		}
	}

	if !shouldSend {
		return
	}

	go func(ev protocol.AlertEvent) {
		sendCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		subject := fmt.Sprintf("[StarMoe Status][%s] %s: %s -> %s", ev.Severity, ev.MonitorName, ev.PrevState, ev.NextState)
		body := fmt.Sprintf("Monitor: %s (%s)\nCluster: %s\nSeverity: %s\nTransition: %s -> %s\nSummary: %s\nTime: %s\nDashboard: %s\n",
			ev.MonitorName, ev.MonitorID, ev.Cluster, ev.Severity, ev.PrevState, ev.NextState,
			ev.Summary, time.Unix(ev.CreatedAt, 0).UTC().Format(time.RFC3339), e.publicBaseURL)

		err := e.email.Send(sendCtx, subject, body)
		status := "sent"
		errStr := ""
		if err != nil {
			status = "failed"
			errStr = err.Error()
			e.logger.Error("email alert failed", "event_id", ev.ID, "err", err)
		}
		_, _ = e.store.InsertAlertDelivery(sendCtx, ev.ID, "email", status, errStr, 1, 0, time.Now().Unix())
	}(ev)
}

// SendTestAlert triggers a synthetic test alert to bot SSE, email, or both.
func (e *Engine) SendTestAlert(ctx context.Context, req protocol.TestAlertRequest) map[string]string {
	results := make(map[string]string)
	sev := req.Severity
	if sev == "" {
		sev = protocol.SeverityP1
	}
	msg := req.Message
	if msg == "" {
		msg = "Manual test alert from StarMoe Status admin panel"
	}
	now := time.Now().Unix()

	ch := req.Channel
	if ch == "" {
		ch = "all"
	}

	if ch == "bot" || ch == "all" {
		ev := protocol.AlertEvent{
			ID:          0,
			MonitorID:   "test-alert",
			MonitorName: "Test Alert",
			Severity:    sev,
			PrevState:   protocol.StateUp,
			NextState:   protocol.StateDegraded,
			Summary:     msg,
			CreatedAt:   now,
		}
		e.Broadcast(ev)
		online, clients, _ := e.BotStatus()
		if online {
			results["bot"] = fmt.Sprintf("broadcasted to %d client(s)", clients)
		} else {
			results["bot"] = "broadcasted (0 clients connected)"
		}
	}

	if ch == "email" || ch == "all" {
		if !e.email.Enabled() {
			results["email"] = "skipped (email not configured)"
		} else {
			if err := e.email.Send(ctx, "[StarMoe Status][TEST] "+sev, msg); err != nil {
				results["email"] = "error: " + err.Error()
			} else {
				results["email"] = "sent"
			}
		}
	}
	return results
}
