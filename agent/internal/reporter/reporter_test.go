package reporter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
)

func TestReporterPush_RetrySuccess(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer secret-cluster-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var payload protocol.HeartbeatPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if payload.Cluster != "DE" {
			t.Errorf("expected cluster DE, got %s", payload.Cluster)
		}

		if attempts.Add(1) < 2 {
			http.Error(w, "temporary error", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	rep := New(srv.URL, "secret-cluster-token", "test", srv.Client())
	rep.baseBackoff = 10 * time.Millisecond

	err := rep.Push(context.Background(), protocol.HeartbeatPayload{
		Cluster:   "DE",
		Timestamp: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts.Load())
	}
}

func TestReporterPush_NoRetryOnUnauthorized(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "invalid token", http.StatusUnauthorized)
	}))
	defer srv.Close()

	rep := New(srv.URL, "wrong-token", "test", srv.Client())
	rep.baseBackoff = 10 * time.Millisecond

	err := rep.Push(context.Background(), protocol.HeartbeatPayload{Cluster: "US"})
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected 1 attempt without retry on 401, got %d", attempts.Load())
	}
}
