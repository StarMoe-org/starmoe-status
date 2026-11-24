package ingest_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
	"github.com/StarMoe-org/starmoe-status/server/internal/alert"
	"github.com/StarMoe-org/starmoe-status/server/internal/config"
	"github.com/StarMoe-org/starmoe-status/server/internal/ingest"
	"github.com/StarMoe-org/starmoe-status/server/internal/store"
)

func TestEvaluateNodeHealth(t *testing.T) {
	// Healthy
	st, _ := ingest.EvaluateNodeHealth(protocol.HeartbeatPayload{
		Cluster:     "DE",
		HostMetrics: protocol.HostMetrics{RootDiskPct: 70, MemUsedPct: 50},
	})
	if st != protocol.StateUp {
		t.Fatalf("expected up, got %s", st)
	}

	// Disk 86% -> degraded
	st, _ = ingest.EvaluateNodeHealth(protocol.HeartbeatPayload{
		Cluster:     "DE",
		HostMetrics: protocol.HostMetrics{RootDiskPct: 86},
	})
	if st != protocol.StateDegraded {
		t.Fatalf("expected degraded for 86%% disk, got %s", st)
	}

	// Invalid extra-storage mount -> down
	st, _ = ingest.EvaluateNodeHealth(protocol.HeartbeatPayload{
		Cluster: "DE",
		HostMetrics: protocol.HostMetrics{
			RootDiskPct: 60,
			Mounts:      []protocol.MountCheck{{Path: "/mnt/extra-storage", Device: "/dev/vda1", ExpectedDevice: "/dev/vdb1", OK: false}},
		},
	})
	if st != protocol.StateDown {
		t.Fatalf("expected down for invalid mount, got %s", st)
	}
}

func TestHeartbeatAndDeadMansSwitch(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "ingest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := &config.Config{
		EmailPolicy:          "off",
		HeartbeatDegradedSec: 120,
		HeartbeatDownSec:     180,
		Monitors: []config.MonitorConfig{
			{ID: "infra-us", Name: "美国节点", GroupKey: "infra", Kind: protocol.KindAgentNode, Cluster: "US", Visibility: "public", Severity: "P0"},
			{ID: "pjsk-servers", Name: "游戏数据服务", GroupKey: "moesekai", Kind: protocol.KindAgentDeep, Cluster: "US", Visibility: "public", Severity: "P0"},
		},
	}
	if err := st.SyncMonitors(ctx, cfg.Monitors); err != nil {
		t.Fatal(err)
	}

	al := alert.New(st, cfg, nil)
	in := ingest.New(st, al, cfg, nil)

	t0 := time.Now().Unix()
	sess := 2
	if err := in.ProcessHeartbeat(ctx, protocol.HeartbeatPayload{
		Cluster:     "US",
		Timestamp:   t0,
		HostMetrics: protocol.HostMetrics{CPULoad1: 1.2, MemUsedPct: 60, RootDiskPct: 50},
		DeepChecks: []protocol.DeepCheckResult{
			{
				MonitorID: "pjsk-servers",
				State:     protocol.StateUp,
				SubItems: []protocol.SubItemStatus{
					{Key: "jp", Label: "日服 (JP)", State: protocol.StateUp, ActiveSessions: &sess},
				},
			},
		},
	}, t0); err != nil {
		t.Fatalf("ProcessHeartbeat: %v", err)
	}

	infraUS, _ := st.GetMonitor(ctx, "infra-us")
	if infraUS.CurrentState != protocol.StateUp {
		t.Fatalf("expected infra-us up, got %s", infraUS.CurrentState)
	}

	// Simulate 200s elapsed (> 180s down threshold) -> Dead-Man's-Switch triggers down + topology suppression.
	if err := in.CheckDeadMansSwitch(ctx, t0+200); err != nil {
		t.Fatalf("CheckDeadMansSwitch: %v", err)
	}
	infraUS, _ = st.GetMonitor(ctx, "infra-us")
	if infraUS.CurrentState != protocol.StateDown {
		t.Fatalf("expected infra-us down after DMS timeout, got %s", infraUS.CurrentState)
	}
	if !al.IsClusterDown("US") {
		t.Fatalf("expected topology suppression enabled for US")
	}
}
