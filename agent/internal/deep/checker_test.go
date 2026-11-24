package deep

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
)

type fakePodChecker struct {
	ready int
	total int
}

func (f fakePodChecker) CheckPodsAlive(_ context.Context, _, _ string) (int, int, error) {
	return f.ready, f.total, nil
}

func TestDeepChecks(t *testing.T) {
	fixedNow := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sekai":
			_, _ = w.Write([]byte(`{
				"servers": {
					"jp": {"active_sessions": 3, "last_login_error": null},
					"cn": {"active_sessions": 1, "last_login_error": null},
					"tw": {"active_sessions": 0, "last_login_error": "timeout"}
				}
			}`))
		case "/masterdata":
			_, _ = w.Write([]byte(`{
				"consecutiveFailures": 4,
				"lastSuccess": "2026-11-24T11:50:00Z"
			}`))
		case "/metabuilder":
			_, _ = w.Write([]byte(`{
				"status": "ok",
				"selfTest": {"failed": ["music_meta"]}
			}`))
		case "/storysearch":
			_, _ = w.Write([]byte(`{
				"ok": true,
				"ready": true,
				"regions": {"jp": {"loaded": true}, "en": {"loaded": false}}
			}`))
		case "/ping":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("pong"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	checker := NewChecker(srv.Client(), fakePodChecker{ready: 2, total: 2})
	checker.now = func() time.Time { return fixedNow }

	results := checker.RunAll(context.Background(), []CheckConfig{
		{ID: "pjsk-servers", Kind: "sekai_health", URL: srv.URL + "/sekai"},
		{ID: "masterdata-jp", Kind: "masterdata_sync", URL: srv.URL + "/masterdata"},
		{ID: "meta-builder", Kind: "meta_builder", URL: srv.URL + "/metabuilder"},
		{ID: "story-search", Kind: "story_search", URL: srv.URL + "/storysearch"},
		{ID: "pot-provider", Kind: "http", URL: srv.URL + "/ping"},
		{ID: "rsshub", Kind: "tcp", Addr: ln.Addr().String()},
		{ID: "vpngate", Kind: "pod_alive", Namespace: "vpngate-proxy", MinReady: 2},
	})

	if len(results) != 7 {
		t.Fatalf("expected 7 results, got %d", len(results))
	}
	if results[0].State != protocol.StateDown || len(results[0].SubItems) != 3 {
		t.Fatalf("unexpected sekai result: %+v", results[0])
	}
	if results[1].State != protocol.StateDegraded {
		t.Fatalf("expected masterdata degraded, got %+v", results[1])
	}
	if results[2].State != protocol.StateDegraded {
		t.Fatalf("expected metabuilder degraded, got %+v", results[2])
	}
	if results[3].State != protocol.StateDegraded {
		t.Fatalf("expected storysearch degraded, got %+v", results[3])
	}
	if results[4].State != protocol.StateUp {
		t.Fatalf("expected pot-provider up, got %+v", results[4])
	}
	if results[5].State != protocol.StateUp {
		t.Fatalf("expected rsshub tcp up, got %+v", results[5])
	}
	if results[6].State != protocol.StateUp {
		t.Fatalf("expected vpngate pod_alive up, got %+v", results[6])
	}
}
