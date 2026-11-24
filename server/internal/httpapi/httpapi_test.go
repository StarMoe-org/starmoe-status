package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
	"github.com/StarMoe-org/starmoe-status/server/internal/alert"
	"github.com/StarMoe-org/starmoe-status/server/internal/auth"
	"github.com/StarMoe-org/starmoe-status/server/internal/config"
	"github.com/StarMoe-org/starmoe-status/server/internal/httpapi"
	"github.com/StarMoe-org/starmoe-status/server/internal/ingest"
	"github.com/StarMoe-org/starmoe-status/server/internal/store"
	"github.com/StarMoe-org/starmoe-status/server/webdist"
)

func setupTestServer(t *testing.T) (*httptest.Server, *store.Store, *alert.Engine) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	deHash, _ := config.ParseTokenHash("de-secret-token")
	usHash, _ := config.ParseTokenHash("us-secret-token")
	botHash, _ := config.ParseTokenHash("bot-secret-token")
	adminHash, _ := config.ParseTokenHash("admin-dev-secret")

	cfg := &config.Config{
		PublicBaseURL: "https://status.star.moe",
		EmailPolicy:   "off",
		ClusterTokenHashes: map[string][32]byte{
			"DE": deHash,
			"US": usHash,
		},
		BotTokenHash:      &botHash,
		AdminDevTokenHash: &adminHash,
		Groups: []config.GroupConfig{
			{Key: "moesekai", Name: "MoeSekai (PJSK)"},
		},
		Monitors: []config.MonitorConfig{
			{ID: "pjsk-servers", Name: "游戏数据服务", GroupKey: "moesekai", Kind: protocol.KindAgentDeep, Cluster: "US", Visibility: "public", Severity: "P0", SortOrder: 1},
			{ID: "infra-us", Name: "美国节点", GroupKey: "infra", Kind: protocol.KindAgentNode, Cluster: "US", Visibility: "public", Severity: "P0", SortOrder: 2},
			{ID: "internal-sll", Name: "sll.star.moe (swp-server)", GroupKey: "internal", Kind: protocol.KindHTTP, Cluster: "DE", Visibility: "internal", Severity: "P1", SortOrder: 3},
			{ID: "internal-bili", Name: "bili.pjsk.moe (moe-bili-sync)", GroupKey: "internal", Kind: protocol.KindHTTP, Cluster: "JP2", Visibility: "internal", Severity: "P1", SortOrder: 4},
		},
	}
	if err := st.SyncMonitors(ctx, cfg.Monitors); err != nil {
		t.Fatal(err)
	}

	au := auth.New(cfg)
	al := alert.New(st, cfg, nil)
	in := ingest.New(st, al, cfg, nil)
	srv := httpapi.New(cfg, st, au, al, in, webdist.FS, nil)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, al
}

func TestPublicAPIRedactionAndCORS(t *testing.T) {
	ts, _, _ := setupTestServer(t)

	// Push US heartbeat containing internal details (pod names, active_sessions, last_error).
	sess := 3
	hb := protocol.HeartbeatPayload{
		Cluster:     "US",
		Timestamp:   time.Now().Unix(),
		HostMetrics: protocol.HostMetrics{CPULoad1: 2.5, MemUsedPct: 72.1, RootDiskPct: 61.2},
		UnhealthyPods: []protocol.UnhealthyPod{
			{Namespace: "secret-ns", Name: "secret-pod-xyz", Phase: "CrashLoopBackOff", Ready: "0/1", Restarts: 5},
		},
		DeepChecks: []protocol.DeepCheckResult{
			{
				MonitorID: "pjsk-servers",
				State:     protocol.StateUp,
				SubItems: []protocol.SubItemStatus{
					{Key: "jp", Label: "日服 (JP)", State: protocol.StateUp, ActiveSessions: &sess, LastError: "internal-upstream-err"},
				},
			},
		},
	}
	hbBytes, _ := json.Marshal(hb)

	// Wrong cluster token (DE token for US cluster) must fail with 401.
	reqBad, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/ingest/heartbeat", bytes.NewReader(hbBytes))
	reqBad.Header.Set("Authorization", "Bearer de-secret-token")
	respBad, err := http.DefaultClient.Do(reqBad)
	if err != nil {
		t.Fatal(err)
	}
	_ = respBad.Body.Close()
	if respBad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for mismatched cluster token, got %d", respBad.StatusCode)
	}

	// Valid US token must succeed.
	reqOK, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/ingest/heartbeat", bytes.NewReader(hbBytes))
	reqOK.Header.Set("Authorization", "Bearer us-secret-token")
	respOK, err := http.DefaultClient.Do(reqOK)
	if err != nil {
		t.Fatal(err)
	}
	_ = respOK.Body.Close()
	if respOK.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid US heartbeat, got %d", respOK.StatusCode)
	}

	// Fetch /api/v1/summary with allowed Origin.
	reqSum, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/summary", nil)
	reqSum.Header.Set("Origin", "https://pjsk.moe")
	respSum, err := http.DefaultClient.Do(reqSum)
	if err != nil {
		t.Fatal(err)
	}
	defer respSum.Body.Close()
	if respSum.StatusCode != http.StatusOK {
		t.Fatalf("summary status %d", respSum.StatusCode)
	}
	if got := respSum.Header.Get("Access-Control-Allow-Origin"); got != "https://pjsk.moe" {
		t.Fatalf("expected CORS header for https://pjsk.moe, got %q", got)
	}
	if cc := respSum.Header.Get("Cache-Control"); !strings.Contains(cc, "s-maxage=30") {
		t.Fatalf("expected Cache-Control with s-maxage=30, got %q", cc)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(respSum.Body)
	rawBody := buf.String()

	// Strictly zero internal leaks!
	forbidden := []string{
		"internal-sll", "sll.star.moe",
		"internal-bili", "bili.pjsk.moe",
		"secret-pod-xyz", "secret-ns",
		"active_sessions", "internal-upstream-err",
		"72.1",
	}
	for _, needle := range forbidden {
		if strings.Contains(rawBody, needle) {
			t.Fatalf("public summary leaked forbidden string %q in body: %s", needle, rawBody)
		}
	}

	// Querying history of an internal monitor must return 404.
	respHist, err := http.Get(ts.URL + "/api/v1/monitors/internal-sll/history")
	if err != nil {
		t.Fatal(err)
	}
	_ = respHist.Body.Close()
	if respHist.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for internal monitor history, got %d", respHist.StatusCode)
	}

	// Disallowed CORS origin must not be echoed.
	reqEvil, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/summary", nil)
	reqEvil.Header.Set("Origin", "https://evil.example.com")
	respEvil, _ := http.DefaultClient.Do(reqEvil)
	_ = respEvil.Body.Close()
	if got := respEvil.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected CORS header for evil origin: %q", got)
	}
}

func TestBotSSEAndAdminEndpoints(t *testing.T) {
	ts, st, al := setupTestServer(t)

	// Unauthenticated /api/bot/stream -> 401
	respNoAuth, _ := http.Get(ts.URL + "/api/bot/stream")
	_ = respNoAuth.Body.Close()
	if respNoAuth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on bot stream without auth, got %d", respNoAuth.StatusCode)
	}

	// Connect authenticated SSE client
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reqSSE, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/bot/stream", nil)
	reqSSE.Header.Set("Authorization", "Bearer bot-secret-token")
	respSSE, err := http.DefaultClient.Do(reqSSE)
	if err != nil {
		t.Fatal(err)
	}
	defer respSSE.Body.Close()
	if respSSE.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on bot SSE, got %d", respSSE.StatusCode)
	}

	reader := bufio.NewReader(respSSE.Body)
	// Read initial ": connected" comment line
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "connected") {
		t.Fatalf("expected connected comment, got %q (err=%v)", line, err)
	}

	// Trigger a state transition and verify SSE receives it.
	mon, _ := st.GetMonitor(context.Background(), "pjsk-servers")
	if _, err := al.RecordTransition(context.Background(), mon, protocol.StateUp, protocol.StateDown, "sekai outage", nil); err != nil {
		t.Fatal(err)
	}

	var dataLine string
	for i := 0; i < 10; i++ {
		l, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE: %v", err)
		}
		if strings.HasPrefix(l, "data: ") {
			dataLine = strings.TrimPrefix(strings.TrimSpace(l), "data: ")
			break
		}
	}
	var gotEv protocol.AlertEvent
	if err := json.Unmarshal([]byte(dataLine), &gotEv); err != nil {
		t.Fatalf("unmarshal SSE data %q: %v", dataLine, err)
	}
	if gotEv.MonitorID != "pjsk-servers" || gotEv.NextState != protocol.StateDown {
		t.Fatalf("unexpected SSE event: %+v", gotEv)
	}

	// Test Admin overview + incident creation + RSS
	reqAdmin, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/admin/overview", nil)
	reqAdmin.Header.Set("Authorization", "Bearer admin-dev-secret")
	respAdmin, err := http.DefaultClient.Do(reqAdmin)
	if err != nil {
		t.Fatal(err)
	}
	defer respAdmin.Body.Close()
	if respAdmin.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on admin overview, got %d", respAdmin.StatusCode)
	}
	var ov protocol.AdminOverview
	if err := json.NewDecoder(respAdmin.Body).Decode(&ov); err != nil {
		t.Fatal(err)
	}
	if !ov.BotOnline || len(ov.Monitors) != 4 {
		t.Fatalf("expected bot online & 4 monitors in admin overview, got online=%v mons=%d", ov.BotOnline, len(ov.Monitors))
	}

	// Create incident
	incBody := `{"type":"incident","title":"API Latency","status":"investigating","impact":"minor","affected_monitors":["pjsk-servers"],"message":"Looking into it"}`
	reqInc, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/admin/incidents", strings.NewReader(incBody))
	reqInc.Header.Set("Authorization", "Bearer admin-dev-secret")
	respInc, err := http.DefaultClient.Do(reqInc)
	if err != nil {
		t.Fatal(err)
	}
	_ = respInc.Body.Close()
	if respInc.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 on create incident, got %d", respInc.StatusCode)
	}

	// Check /rss.xml and /healthz
	respRSS, err := http.Get(ts.URL + "/rss.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer respRSS.Body.Close()
	var rssBuf bytes.Buffer
	_, _ = rssBuf.ReadFrom(respRSS.Body)
	if respRSS.StatusCode != http.StatusOK || !strings.Contains(rssBuf.String(), "API Latency") {
		t.Fatalf("unexpected RSS response: status=%d body=%s", respRSS.StatusCode, rssBuf.String())
	}

	respHZ, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = respHZ.Body.Close()
	if respHZ.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on /healthz, got %d", respHZ.StatusCode)
	}
}
