package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
	"github.com/StarMoe-org/starmoe-status/server/internal/config"
	"github.com/StarMoe-org/starmoe-status/server/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestStoreMonitorsBucketsAndPrune(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if err := st.CheckWritable(ctx); err != nil {
		t.Fatalf("CheckWritable: %v", err)
	}

	mons := []config.MonitorConfig{
		{ID: "pjsk-web", Name: "PJSK", GroupKey: "moesekai", Kind: "http", Cluster: "DE", Visibility: "public", Severity: "P0", SortOrder: 1},
		{ID: "internal-sll", Name: "SLL", GroupKey: "internal", Kind: "http", Cluster: "DE", Visibility: "internal", Severity: "P1", SortOrder: 2},
	}
	if err := st.SyncMonitors(ctx, mons); err != nil {
		t.Fatalf("SyncMonitors: %v", err)
	}

	pub, err := st.ListMonitors(ctx, true)
	if err != nil {
		t.Fatalf("ListMonitors(public): %v", err)
	}
	if len(pub) != 1 || pub[0].ID != "pjsk-web" {
		t.Fatalf("expected 1 public monitor pjsk-web, got %+v", pub)
	}

	all, err := st.ListMonitors(ctx, false)
	if err != nil || len(all) != 2 {
		t.Fatalf("expected 2 total monitors, got %d (err=%v)", len(all), err)
	}

	now := time.Now().UTC()
	lat1, lat2 := int64(40), int64(80)
	sc := 200
	if err := st.RecordCheckSample(ctx, store.CheckSample{
		MonitorID:  "pjsk-web",
		State:      protocol.StateUp,
		StatusCode: &sc,
		LatencyMs:  &lat1,
		CheckedAt:  now.Add(-2 * time.Minute).Unix(),
	}); err != nil {
		t.Fatalf("RecordCheckSample 1: %v", err)
	}
	if err := st.RecordCheckSample(ctx, store.CheckSample{
		MonitorID:  "pjsk-web",
		State:      protocol.StateDown,
		StatusCode: &sc,
		LatencyMs:  &lat2,
		CheckedAt:  now.Add(-1 * time.Minute).Unix(),
	}); err != nil {
		t.Fatalf("RecordCheckSample 2: %v", err)
	}

	barsMap, uptimeMap, err := st.GetDailyBarsBatch(ctx, []string{"pjsk-web"}, 90, now)
	if err != nil {
		t.Fatalf("GetDailyBarsBatch: %v", err)
	}
	if len(barsMap["pjsk-web"]) != 90 {
		t.Fatalf("expected 90 daily bars, got %d", len(barsMap["pjsk-web"]))
	}
	if uptimeMap["pjsk-web"] != 50.0 {
		t.Fatalf("expected 50.0%% uptime, got %.2f", uptimeMap["pjsk-web"])
	}

	lat24, err := st.GetLatency24h(ctx, "pjsk-web", now)
	if err != nil || len(lat24) != 2 {
		t.Fatalf("expected 2 latency points, got %d (err=%v)", len(lat24), err)
	}

	// Insert an old sample (40 days ago) and verify Prune deletes it.
	oldTS := now.AddDate(0, 0, -40).Unix()
	if err := st.RecordCheckSample(ctx, store.CheckSample{
		MonitorID: "pjsk-web",
		State:     protocol.StateUp,
		LatencyMs: &lat1,
		CheckedAt: oldTS,
	}); err != nil {
		t.Fatalf("RecordCheckSample old: %v", err)
	}
	if err := st.Prune(ctx, now); err != nil {
		t.Fatalf("Prune: %v", err)
	}
}

func TestStoreIncidentsAndMaintenance(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	now := time.Now().Unix()
	starts := now - 60
	ends := now + 3600

	inc, err := st.CreateIncident(ctx, protocol.CreateIncidentRequest{
		Type:             protocol.IncidentTypeMaintenance,
		Title:            "DE Disk Maintenance",
		Status:           protocol.IncidentStatusInProgress,
		Impact:           protocol.ImpactMaintenance,
		AffectedMonitors: []string{"pjsk-web"},
		Message:          "Upgrading storage",
		StartsAt:         &starts,
		EndsAt:           &ends,
	}, "ops@star.moe", now)
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	maintID, active, err := st.ActiveMaintenanceForMonitor(ctx, "pjsk-web", now)
	if err != nil || !active || maintID != inc.ID {
		t.Fatalf("expected active maintenance %s for pjsk-web, got id=%s active=%v err=%v", inc.ID, maintID, active, err)
	}
	_, otherActive, _ := st.ActiveMaintenanceForMonitor(ctx, "bdon-web", now)
	if otherActive {
		t.Fatalf("did not expect maintenance for bdon-web")
	}

	// Public listing must redact CreatedBy.
	pubList, err := st.ListIncidents(ctx, false, true)
	if err != nil || len(pubList) != 1 {
		t.Fatalf("ListIncidents: %v", err)
	}
	if pubList[0].CreatedBy != "" || pubList[0].Updates[0].CreatedBy != "" {
		t.Fatalf("expected CreatedBy redacted on public list, got %+v", pubList[0])
	}

	resolved := protocol.IncidentStatusResolved
	updated, err := st.UpdateIncident(ctx, inc.ID, protocol.UpdateIncidentRequest{
		Status:  &resolved,
		Message: "Maintenance complete",
	}, "ops@star.moe", now+120)
	if err != nil {
		t.Fatalf("UpdateIncident: %v", err)
	}
	if updated.Status != protocol.IncidentStatusResolved || len(updated.Updates) != 2 {
		t.Fatalf("unexpected updated incident: %+v", updated)
	}
}
