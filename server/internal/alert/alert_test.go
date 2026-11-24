package alert_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
	"github.com/StarMoe-org/starmoe-status/server/internal/alert"
	"github.com/StarMoe-org/starmoe-status/server/internal/config"
	"github.com/StarMoe-org/starmoe-status/server/internal/store"
)

func TestTopologySuppressionAndMaintenanceSilence(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "alert.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := &config.Config{
		EmailPolicy: "off",
		Monitors: []config.MonitorConfig{
			{ID: "infra-de", Name: "德国节点", GroupKey: "infra", Kind: protocol.KindAgentNode, Cluster: "DE", Visibility: "public", Severity: "P0"},
			{ID: "pjsk-web", Name: "PJSK", GroupKey: "moesekai", Kind: protocol.KindHTTP, Cluster: "DE", Visibility: "public", Severity: "P0"},
			{ID: "bdon-web", Name: "BDON", GroupKey: "moenotes", Kind: protocol.KindHTTP, Cluster: "JP1", Visibility: "public", Severity: "P0"},
		},
	}
	if err := st.SyncMonitors(ctx, cfg.Monitors); err != nil {
		t.Fatal(err)
	}

	eng := alert.New(st, cfg, nil)
	subID, ch := eng.Subscribe()
	defer eng.Unsubscribe(subID)

	online, clients, _ := eng.BotStatus()
	if !online || clients != 1 {
		t.Fatalf("expected bot online with 1 client, got online=%v clients=%d", online, clients)
	}

	// 1. Mark DE infra node down -> should broadcast and enable topology suppression for DE.
	infraDE, _ := st.GetMonitor(ctx, "infra-de")
	evNode, err := eng.RecordTransition(ctx, infraDE, protocol.StateUp, protocol.StateDown, "DE lost", nil)
	if err != nil {
		t.Fatal(err)
	}
	if evNode.SuppressedBy != "" {
		t.Fatalf("node down should not be suppressed, got %q", evNode.SuppressedBy)
	}
	select {
	case got := <-ch:
		if got.MonitorID != "infra-de" {
			t.Fatalf("unexpected SSE event: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for infra-de SSE event")
	}

	// 2. Child service on DE goes down -> must be suppressed by node-down:DE and NOT broadcast.
	pjskWeb, _ := st.GetMonitor(ctx, "pjsk-web")
	evChild, err := eng.RecordTransition(ctx, pjskWeb, protocol.StateUp, protocol.StateDown, "pjsk timeout", nil)
	if err != nil {
		t.Fatal(err)
	}
	if evChild.SuppressedBy != "node-down:DE" {
		t.Fatalf("expected suppressed_by=node-down:DE, got %q", evChild.SuppressedBy)
	}
	select {
	case got := <-ch:
		t.Fatalf("suppressed event should not be broadcast, got %+v", got)
	case <-time.After(50 * time.Millisecond):
	}

	// 3. Maintenance window on bdon-web -> must be suppressed by maintenance:<id>.
	now := time.Now().Unix()
	starts, ends := now-60, now+600
	inc, err := st.CreateIncident(ctx, protocol.CreateIncidentRequest{
		Type:             protocol.IncidentTypeMaintenance,
		Title:            "JP1 Maint",
		Status:           protocol.IncidentStatusInProgress,
		AffectedMonitors: []string{"bdon-web"},
		StartsAt:         &starts,
		EndsAt:           &ends,
	}, "admin@star.moe", now)
	if err != nil {
		t.Fatal(err)
	}

	bdonWeb, _ := st.GetMonitor(ctx, "bdon-web")
	evMaint, err := eng.RecordTransition(ctx, bdonWeb, protocol.StateUp, protocol.StateDown, "restarting", nil)
	if err != nil {
		t.Fatal(err)
	}
	if evMaint.SuppressedBy != "maintenance:"+inc.ID {
		t.Fatalf("expected suppressed_by=maintenance:%s, got %q", inc.ID, evMaint.SuppressedBy)
	}
}
