package vm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCollectHostMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query().Get("query")
		var resp queryResponse
		resp.Status = "success"
		resp.Data.ResultType = "vector"

		switch {
		case q == "node_load1":
			resp.Data.Result = []querySample{{Value: []any{1700000000.0, "1.25"}}}
		case strings.Contains(q, "node_cpu_seconds_total"):
			resp.Data.Result = []querySample{{Value: []any{1700000000.0, "4"}}}
		case strings.Contains(q, "MemAvailable"):
			resp.Data.Result = []querySample{{Value: []any{1700000000.0, "62.5"}}}
		case strings.Contains(q, "SwapTotal"):
			resp.Data.Result = []querySample{{Value: []any{1700000000.0, "12.0"}}}
		case strings.Contains(q, `mountpoint="/"`):
			resp.Data.Result = []querySample{{Value: []any{1700000000.0, "78.4"}}}
		case strings.Contains(q, `node_filesystem_size_bytes{mountpoint="/mnt/extra-storage"`):
			resp.Data.Result = []querySample{{
				Metric: map[string]string{"device": "/dev/vdb1", "mountpoint": "/mnt/extra-storage"},
				Value:  []any{1700000000.0, "1000"},
			}}
		case strings.Contains(q, `node_filesystem_avail_bytes{mountpoint="/mnt/extra-storage"`):
			resp.Data.Result = []querySample{{
				Metric: map[string]string{"device": "/dev/vdb1", "mountpoint": "/mnt/extra-storage"},
				Value:  []any{1700000000.0, "250"},
			}}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	m, err := c.CollectHostMetrics(context.Background(), "/mnt/extra-storage", "/dev/vdb1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.CPULoad1 != 1.25 || m.CPUCores != 4 {
		t.Fatalf("unexpected cpu: %+v", m)
	}
	if m.MemUsedPct != 62.5 || m.MemAvailablePct != 37.5 {
		t.Fatalf("unexpected mem: %+v", m)
	}
	if m.SwapUsedPct != 12.0 || m.RootDiskPct != 78.4 {
		t.Fatalf("unexpected swap/root: %+v", m)
	}
	if m.ExtraDiskPct != 75.0 {
		t.Fatalf("expected extra disk 75.0, got %v", m.ExtraDiskPct)
	}
	if len(m.Mounts) != 1 || !m.Mounts[0].OK || m.Mounts[0].Device != "/dev/vdb1" {
		t.Fatalf("unexpected mounts: %+v", m.Mounts)
	}
}

func TestCollectHostMetrics_MountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		var resp queryResponse
		resp.Status = "success"
		if strings.Contains(q, `node_filesystem_size_bytes{mountpoint="/mnt/extra-storage"`) {
			resp.Data.Result = []querySample{{
				Metric: map[string]string{"device": "/dev/vda1"},
				Value:  []any{1700000000.0, "1000"},
			}}
		} else if q == "node_load1" {
			resp.Data.Result = []querySample{{Value: []any{1700000000.0, "0.5"}}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	m, err := c.CollectHostMetrics(context.Background(), "/mnt/extra-storage", "/dev/vdb1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Mounts) != 1 || m.Mounts[0].OK {
		t.Fatalf("expected mount check failure, got %+v", m.Mounts)
	}
}
