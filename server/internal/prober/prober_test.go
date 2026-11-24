package prober_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
	"github.com/StarMoe-org/starmoe-status/server/internal/alert"
	"github.com/StarMoe-org/starmoe-status/server/internal/config"
	"github.com/StarMoe-org/starmoe-status/server/internal/prober"
	"github.com/StarMoe-org/starmoe-status/server/internal/store"
)

func TestEvaluateJSONAssert(t *testing.T) {
	cases := []struct {
		body string
		expr string
		want bool
	}{
		{`{"redis":true}`, "redis==true", true},
		{`{"redis":false}`, "redis==true", false},
		{`{"ok":true,"ready":true}`, "ok&&ready", true},
		{`{"ok":true,"ready":false}`, "ok&&ready", false},
		{`{"status":"ok","nested":{"val":1}}`, "status==ok&&nested.val==1", true},
		{`{"status":"error"}`, "status!=error", false},
		{`not-json`, "ok", false},
	}
	for _, tc := range cases {
		got, reason := prober.EvaluateJSONAssert([]byte(tc.body), tc.expr)
		if got != tc.want {
			t.Errorf("EvaluateJSONAssert(%s, %q) = %v (%s), want %v", tc.body, tc.expr, got, reason, tc.want)
		}
	}
}

func TestProberHTTPAndDebounce(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var status int = http.StatusNoContent
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer ts.Close()

	monCfg := config.MonitorConfig{
		ID:             "starmoe-passport",
		Name:           "Passport",
		GroupKey:       "platform",
		Kind:           protocol.KindHTTP,
		Cluster:        "US",
		Visibility:     protocol.VisibilityPublic,
		Severity:       protocol.SeverityP0,
		URL:            ts.URL,
		Method:         http.MethodGet,
		ExpectedStatus: []int{204},
		TimeoutSec:     5,
		FailThreshold:  3,
		OKThreshold:    2,
		SortOrder:      1,
	}
	cfg := &config.Config{
		EmailPolicy: "off",
		Monitors:    []config.MonitorConfig{monCfg},
	}
	if err := st.SyncMonitors(ctx, cfg.Monitors); err != nil {
		t.Fatal(err)
	}

	al := alert.New(st, cfg, nil)
	pr := prober.New(st, al, cfg, nil)

	// 1. Initial OK -> transitions unknown -> up immediately (no alert noise).
	res := pr.ProbeMonitor(ctx, monCfg)
	if res.State != protocol.StateUp {
		t.Fatalf("expected up for 204, got %+v", res)
	}
	now := time.Now().Unix()
	if err := pr.ApplyResult(ctx, monCfg, res, now); err != nil {
		t.Fatal(err)
	}
	mon, _ := st.GetMonitor(ctx, monCfg.ID)
	if mon.CurrentState != protocol.StateUp {
		t.Fatalf("expected up, got %s", mon.CurrentState)
	}

	// 2. Two consecutive 500s should NOT mark down yet (debounce threshold = 3).
	status = http.StatusInternalServerError
	for i := 1; i <= 2; i++ {
		r := pr.ProbeMonitor(ctx, monCfg)
		if err := pr.ApplyResult(ctx, monCfg, r, now+int64(i*60)); err != nil {
			t.Fatal(err)
		}
		mon, _ = st.GetMonitor(ctx, monCfg.ID)
		if mon.CurrentState != protocol.StateUp {
			t.Fatalf("fail #%d should stay up, got %s", i, mon.CurrentState)
		}
	}

	// 3. Third consecutive 500 -> transitions to down and emits 1 state_event.
	r3 := pr.ProbeMonitor(ctx, monCfg)
	if err := pr.ApplyResult(ctx, monCfg, r3, now+180); err != nil {
		t.Fatal(err)
	}
	mon, _ = st.GetMonitor(ctx, monCfg.ID)
	if mon.CurrentState != protocol.StateDown {
		t.Fatalf("fail #3 should be down, got %s", mon.CurrentState)
	}
	events, _ := st.ListStateEventsAfter(ctx, 0, true, 10)
	if len(events) != 1 || events[0].NextState != protocol.StateDown {
		t.Fatalf("expected 1 down event, got %+v", events)
	}

	// 4. Recovery debounce: 1 OK stays down, 2nd OK recovers to up.
	status = http.StatusNoContent
	r4 := pr.ProbeMonitor(ctx, monCfg)
	_ = pr.ApplyResult(ctx, monCfg, r4, now+240)
	mon, _ = st.GetMonitor(ctx, monCfg.ID)
	if mon.CurrentState != protocol.StateDown {
		t.Fatalf("ok #1 should stay down, got %s", mon.CurrentState)
	}

	r5 := pr.ProbeMonitor(ctx, monCfg)
	_ = pr.ApplyResult(ctx, monCfg, r5, now+300)
	mon, _ = st.GetMonitor(ctx, monCfg.ID)
	if mon.CurrentState != protocol.StateUp {
		t.Fatalf("ok #2 should recover to up, got %s", mon.CurrentState)
	}
	events, _ = st.ListStateEventsAfter(ctx, 0, true, 10)
	if len(events) != 2 || events[1].NextState != protocol.StateUp {
		t.Fatalf("expected 2 events (down + up), got %+v", events)
	}
}
