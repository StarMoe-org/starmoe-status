package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
	"github.com/StarMoe-org/starmoe-status/server/internal/alert"
	"github.com/StarMoe-org/starmoe-status/server/internal/config"
	"github.com/StarMoe-org/starmoe-status/server/internal/store"
)

// Ingestor processes agent heartbeats and runs the Dead-Man's-Switch watchdog.
type Ingestor struct {
	store       *store.Store
	alerts      *alert.Engine
	degradedSec int64
	downSec     int64
	logger      *slog.Logger
}

// New creates an Ingestor.
func New(st *store.Store, al *alert.Engine, cfg *config.Config, logger *slog.Logger) *Ingestor {
	if logger == nil {
		logger = slog.Default()
	}
	degSec := int64(cfg.HeartbeatDegradedSec)
	if degSec <= 0 {
		degSec = 120
	}
	downSec := int64(cfg.HeartbeatDownSec)
	if downSec <= 0 {
		downSec = 180
	}
	return &Ingestor{
		store:       st,
		alerts:      al,
		degradedSec: degSec,
		downSec:     downSec,
		logger:      logger,
	}
}

// ProcessHeartbeat stores a cluster heartbeat and updates its infra node + deep check monitors.
func (in *Ingestor) ProcessHeartbeat(ctx context.Context, payload protocol.HeartbeatPayload, receivedAt int64) error {
	payload.Cluster = strings.ToUpper(strings.TrimSpace(payload.Cluster))
	if payload.Cluster == "" {
		return errors.New("missing cluster")
	}
	if payload.Timestamp == 0 {
		payload.Timestamp = receivedAt
	}

	if err := in.store.InsertHeartbeat(ctx, payload, receivedAt); err != nil {
		return fmt.Errorf("insert heartbeat: %w", err)
	}

	// 1. Evaluate cluster node health and update infra-<cluster> monitor.
	nodeState, nodeMsg := EvaluateNodeHealth(payload)
	infraID := "infra-" + strings.ToLower(payload.Cluster)
	if err := in.applyMonitorUpdate(ctx, infraID, nodeState, nil, nodeMsg, "", receivedAt); err != nil && !errors.Is(err, store.ErrNotFound) {
		in.logger.Error("update infra monitor failed", "monitor", infraID, "err", err)
	}

	// 2. Process deep checks reported by the agent.
	for _, dc := range payload.DeepChecks {
		if dc.MonitorID == "" {
			continue
		}
		state := normalizeState(dc.State)
		var lat *int64
		if dc.LatencyMs > 0 {
			l := dc.LatencyMs
			lat = &l
		}
		metaJSON := marshalDeepMeta(dc)
		msg := dc.Message
		if msg == "" {
			msg = state
		}
		if err := in.applyMonitorUpdate(ctx, dc.MonitorID, state, lat, msg, metaJSON, receivedAt); err != nil && !errors.Is(err, store.ErrNotFound) {
			in.logger.Error("update deep monitor failed", "monitor", dc.MonitorID, "err", err)
		}
	}

	return nil
}

// EvaluateNodeHealth evaluates host metrics, mounts, node conditions, pods, certs, and ArgoCD
// according to docs/design.md §5.2.
func EvaluateNodeHealth(p protocol.HeartbeatPayload) (string, string) {
	var downReasons []string
	var degReasons []string

	hm := p.HostMetrics
	if hm.RootDiskPct >= 92 {
		downReasons = append(downReasons, fmt.Sprintf("root disk %.1f%%", hm.RootDiskPct))
	} else if hm.RootDiskPct >= 85 {
		degReasons = append(degReasons, fmt.Sprintf("root disk %.1f%%", hm.RootDiskPct))
	}

	if hm.ExtraDiskPct >= 92 {
		downReasons = append(downReasons, fmt.Sprintf("extra disk %.1f%%", hm.ExtraDiskPct))
	} else if hm.ExtraDiskPct >= 85 {
		degReasons = append(degReasons, fmt.Sprintf("extra disk %.1f%%", hm.ExtraDiskPct))
	}

	memLow := (hm.MemAvailablePct > 0 && hm.MemAvailablePct < 10) || (hm.MemAvailablePct == 0 && hm.MemUsedPct > 90)
	if memLow && hm.SwapUsedPct > 60 {
		degReasons = append(degReasons, fmt.Sprintf("mem %.1f%% swap %.1f%%", hm.MemUsedPct, hm.SwapUsedPct))
	}

	for _, m := range hm.Mounts {
		if !m.OK {
			downReasons = append(downReasons, fmt.Sprintf("mount %s invalid (%s)", m.Path, m.Device))
		}
	}

	for _, cond := range p.NodeConditions {
		switch cond.Type {
		case "Ready":
			if cond.Status != "True" {
				downReasons = append(downReasons, "node NotReady")
			}
		case "DiskPressure":
			if cond.Status == "True" {
				downReasons = append(downReasons, "node DiskPressure")
			}
		case "MemoryPressure":
			if cond.Status == "True" {
				downReasons = append(downReasons, "node MemoryPressure")
			}
		}
	}

	for _, cert := range p.ExpiringCerts {
		if cert.DaysLeft < 3 {
			downReasons = append(downReasons, fmt.Sprintf("cert %s expires in %dd", cert.Name, cert.DaysLeft))
		} else if !cert.Ready || cert.DaysLeft < 14 {
			degReasons = append(degReasons, fmt.Sprintf("cert %s (%dd)", cert.Name, cert.DaysLeft))
		}
	}

	if len(p.UnhealthyPods) > 0 {
		degReasons = append(degReasons, fmt.Sprintf("%d unhealthy pod(s)", len(p.UnhealthyPods)))
	}
	if len(p.ArgoCDUnhealthy) > 0 {
		degReasons = append(degReasons, fmt.Sprintf("%d ArgoCD app(s) unhealthy", len(p.ArgoCDUnhealthy)))
	}

	if len(downReasons) > 0 {
		return protocol.StateDown, strings.Join(downReasons, "; ")
	}
	if len(degReasons) > 0 {
		return protocol.StateDegraded, strings.Join(degReasons, "; ")
	}
	return protocol.StateUp, "ok"
}

// RunDeadMansSwitch periodically checks for clusters whose heartbeat has timed out.
func (in *Ingestor) RunDeadMansSwitch(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := in.CheckDeadMansSwitch(ctx, time.Now().Unix()); err != nil {
				in.logger.Error("dead-man's-switch check failed", "err", err)
			}
		}
	}
}

// CheckDeadMansSwitch inspects all agent_node monitors against their latest heartbeat timestamp.
func (in *Ingestor) CheckDeadMansSwitch(ctx context.Context, now int64) error {
	monitors, err := in.store.ListMonitors(ctx, false)
	if err != nil {
		return err
	}
	hbs, err := in.store.GetLatestHeartbeats(ctx)
	if err != nil {
		return err
	}

	for _, m := range monitors {
		if m.Kind != protocol.KindAgentNode || m.Cluster == "" {
			continue
		}
		hb, ok := hbs[m.Cluster]
		if !ok {
			// No heartbeat has ever been received for this cluster yet; stay unknown.
			continue
		}
		age := now - hb.ReceivedAt
		if age >= in.downSec {
			msg := fmt.Sprintf("cluster %s heartbeat lost (%ds ago)", m.Cluster, age)
			if err := in.applyMonitorUpdate(ctx, m.ID, protocol.StateDown, nil, msg, "", now); err != nil {
				in.logger.Error("dms mark down failed", "monitor", m.ID, "err", err)
			}
		} else if age >= in.degradedSec && m.CurrentState == protocol.StateUp {
			msg := fmt.Sprintf("cluster %s heartbeat delayed (%ds ago)", m.Cluster, age)
			if err := in.applyMonitorUpdate(ctx, m.ID, protocol.StateDegraded, nil, msg, "", now); err != nil {
				in.logger.Error("dms mark degraded failed", "monitor", m.ID, "err", err)
			}
		}
	}
	return nil
}

func (in *Ingestor) applyMonitorUpdate(ctx context.Context, monitorID, nextState string, latencyMs *int64, message, metaJSON string, ts int64) error {
	cur, err := in.store.GetMonitor(ctx, monitorID)
	if err != nil {
		return err
	}

	if err := in.store.RecordCheckSample(ctx, store.CheckSample{
		MonitorID: monitorID,
		State:     nextState,
		LatencyMs: latencyMs,
		Message:   message,
		CheckedAt: ts,
	}); err != nil {
		return err
	}

	prevState := cur.CurrentState
	if err := in.store.UpdateMonitorState(ctx, monitorID, nextState, 0, 0, latencyMs, message, metaJSON, ts); err != nil {
		return err
	}

	if nextState != prevState {
		if prevState == protocol.StateUnknown && nextState == protocol.StateUp {
			if cur.Kind == protocol.KindAgentNode && cur.Cluster != "" && in.alerts != nil {
				in.alerts.SetClusterDown(cur.Cluster, false)
			}
			return nil
		}
		if in.alerts != nil {
			summary := fmt.Sprintf("%s: %s -> %s (%s)", cur.Name, prevState, nextState, message)
			_, err = in.alerts.RecordTransition(ctx, cur, prevState, nextState, summary, nil)
			return err
		}
	}
	return nil
}

func normalizeState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case protocol.StateUp, "ok", "healthy":
		return protocol.StateUp
	case protocol.StateDegraded, "warn", "warning":
		return protocol.StateDegraded
	case protocol.StateDown, "error", "critical", "unhealthy":
		return protocol.StateDown
	default:
		return protocol.StateUnknown
	}
}

func marshalDeepMeta(dc protocol.DeepCheckResult) string {
	if len(dc.SubItems) == 0 && len(dc.Details) == 0 {
		return ""
	}
	m := make(map[string]any)
	if len(dc.SubItems) > 0 {
		m["sub_items"] = dc.SubItems
	}
	if len(dc.Details) > 0 {
		m["details"] = dc.Details
	}
	b, _ := json.Marshal(m)
	return string(b)
}
