package k8s

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestK8sReadOnlyQueries(t *testing.T) {
	fixedNow := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-sa-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/nodes":
			_, _ = w.Write([]byte(`{
				"items": [{
					"metadata": {"name": "node-1"},
					"status": {
						"conditions": [
							{"type": "Ready", "status": "True"},
							{"type": "DiskPressure", "status": "False"}
						]
					}
				}]
			}`))
		case "/api/v1/pods":
			_, _ = w.Write([]byte(`{
				"items": [
					{
						"metadata": {"namespace": "apps", "name": "healthy-abc"},
						"status": {"phase": "Running", "containerStatuses": [{"name": "app", "ready": true, "restartCount": 0}]}
					},
					{
						"metadata": {"namespace": "apps", "name": "sw-vacuum-12345"},
						"status": {"phase": "Succeeded"}
					},
					{
						"metadata": {"namespace": "default", "name": "py313-test"},
						"status": {"phase": "Failed", "reason": "CrashLoopBackOff"}
					},
					{
						"metadata": {"namespace": "apps", "name": "api-pod-1"},
						"status": {
							"phase": "Running",
							"containerStatuses": [{
								"name": "api",
								"ready": false,
								"restartCount": 5,
								"state": {"waiting": {"reason": "CrashLoopBackOff"}}
							}]
						}
					}
				]
			}`))
		case "/apis/cert-manager.io/v1/certificates":
			_, _ = w.Write([]byte(`{
				"items": [
					{
						"metadata": {"namespace": "apps", "name": "ok-cert"},
						"status": {"notAfter": "2027-02-01T00:00:00Z", "conditions": [{"type": "Ready", "status": "True"}]}
					},
					{
						"metadata": {"namespace": "apps", "name": "soon-cert"},
						"status": {"notAfter": "2026-12-01T00:00:00Z", "conditions": [{"type": "Ready", "status": "True"}]}
					},
					{
						"metadata": {"namespace": "apps", "name": "broken-cert"},
						"status": {"notAfter": "2027-02-01T00:00:00Z", "conditions": [{"type": "Ready", "status": "False"}]}
					}
				]
			}`))
		case "/apis/argoproj.io/v1alpha1/namespaces/argocd/applications":
			_, _ = w.Write([]byte(`{
				"items": [
					{"metadata": {"name": "app-ok"}, "status": {"sync": {"status": "Synced"}, "health": {"status": "Healthy"}}},
					{"metadata": {"name": "app-drift"}, "status": {"sync": {"status": "OutOfSync"}, "health": {"status": "Degraded"}, "health": {"status": "Degraded", "message": "rollout stuck"}}}
				]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-sa-token", srv.Client())
	c.now = func() time.Time { return fixedNow }

	ctx := context.Background()

	conds, err := c.ListNodeConditions(ctx)
	if err != nil || len(conds) != 2 {
		t.Fatalf("unexpected node conditions: %+v, err=%v", conds, err)
	}

	pods, err := c.ListUnhealthyPods(ctx, DefaultExcludedWorkloads)
	if err != nil {
		t.Fatalf("ListUnhealthyPods error: %v", err)
	}
	if len(pods) != 1 || pods[0].Name != "api-pod-1" || pods[0].Reason != "CrashLoopBackOff" || pods[0].Ready != "0/1" {
		t.Fatalf("unexpected unhealthy pods: %+v", pods)
	}

	certs, err := c.ListExpiringCerts(ctx, 14)
	if err != nil {
		t.Fatalf("ListExpiringCerts error: %v", err)
	}
	if len(certs) != 2 {
		t.Fatalf("expected 2 expiring/unready certs, got %+v", certs)
	}
	if certs[0].Name != "soon-cert" || certs[0].DaysLeft != 7 || !certs[0].Ready {
		t.Fatalf("unexpected soon-cert: %+v", certs[0])
	}
	if certs[1].Name != "broken-cert" || certs[1].Ready {
		t.Fatalf("unexpected broken-cert: %+v", certs[1])
	}

	apps, err := c.ListArgoCDUnhealthy(ctx)
	if err != nil {
		t.Fatalf("ListArgoCDUnhealthy error: %v", err)
	}
	if len(apps) != 1 || apps[0].Name != "app-drift" || apps[0].SyncStatus != "OutOfSync" {
		t.Fatalf("unexpected argocd apps: %+v", apps)
	}
}
