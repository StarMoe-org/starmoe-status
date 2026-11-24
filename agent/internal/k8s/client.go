package k8s

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
)

const (
	serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	serviceAccountCAPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

// DefaultExcludedWorkloads lists legacy workloads excluded from unhealthy pod alerts per docs/design.md §4.3.
var DefaultExcludedWorkloads = []string{
	"default/py313-test",
	"apps/moe-ranking-hub-next",
	"apps/moe-sekai-api",
	"apps/mysekai-housing-ranking-api",
	"apps/rip-trigger",
	"apps/ci-relay",
}

// Client is a minimal read-only Kubernetes API client using standard net/http (no client-go bloat).
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	now        func() time.Time
}

// NewInClusterClient constructs a read-only client from the pod's ServiceAccount mount.
func NewInClusterClient() (*Client, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in-cluster: KUBERNETES_SERVICE_HOST/PORT unset")
	}
	tokenBytes, err := os.ReadFile(serviceAccountTokenPath)
	if err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}
	caBytes, err := os.ReadFile(serviceAccountCAPath)
	if err != nil {
		return nil, fmt.Errorf("read service account CA: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("invalid service account CA PEM")
	}

	baseURL := fmt.Sprintf("https://%s:%s", host, port)
	if strings.Contains(host, ":") {
		baseURL = fmt.Sprintf("https://[%s]:%s", host, port)
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				MinVersion: tls.VersionTLS12,
			},
		},
	}

	return &Client{
		baseURL:    baseURL,
		token:      strings.TrimSpace(string(tokenBytes)),
		httpClient: httpClient,
		now:        time.Now,
	}, nil
}

// NewClient creates a Client with explicit URL, token, and http.Client (for tests).
func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		httpClient: httpClient,
		now:        time.Now,
	}
}

func (c *Client) getJSON(ctx context.Context, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("k8s GET %s returned %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return resp.StatusCode, fmt.Errorf("decode %s: %w", path, err)
	}
	return resp.StatusCode, nil
}

// --- Nodes ---

type nodeList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Conditions []protocol.NodeCondition `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

// ListNodeConditions returns node conditions (Ready, DiskPressure, MemoryPressure, PIDPressure).
func (c *Client) ListNodeConditions(ctx context.Context) ([]protocol.NodeCondition, error) {
	var list nodeList
	if _, err := c.getJSON(ctx, "/api/v1/nodes", &list); err != nil {
		return nil, err
	}
	var out []protocol.NodeCondition
	for _, node := range list.Items {
		for _, cond := range node.Status.Conditions {
			out = append(out, protocol.NodeCondition{
				Type:    cond.Type,
				Status:  cond.Status,
				Reason:  cond.Reason,
				Message: cond.Message,
			})
		}
	}
	return out, nil
}

// --- Pods ---

type podList struct {
	Items []podItem `json:"items"`
}

type podItem struct {
	Metadata struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"metadata"`
	Status struct {
		Phase             string `json:"phase"`
		Reason            string `json:"reason"`
		ContainerStatuses []struct {
			Name         string `json:"name"`
			Ready        bool   `json:"ready"`
			RestartCount int    `json:"restartCount"`
			State        struct {
				Waiting *struct {
					Reason string `json:"reason"`
				} `json:"waiting"`
				Terminated *struct {
					Reason string `json:"reason"`
				} `json:"terminated"`
			} `json:"state"`
			LastState struct {
				Terminated *struct {
					Reason string `json:"reason"`
				} `json:"terminated"`
			} `json:"lastState"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

func isExcluded(namespace, name string, excludes []string) bool {
	full := namespace + "/" + name
	for _, ex := range excludes {
		ex = strings.TrimSpace(ex)
		if ex == "" {
			continue
		}
		if full == ex || strings.HasPrefix(full, ex+"-") {
			return true
		}
	}
	return false
}

// ListUnhealthyPods returns pods that are not Ready, not Running, or restarting excessively,
// excluding Completed (Succeeded) job pods and configured exclusion prefixes.
func (c *Client) ListUnhealthyPods(ctx context.Context, excludes []string) ([]protocol.UnhealthyPod, error) {
	var list podList
	if _, err := c.getJSON(ctx, "/api/v1/pods", &list); err != nil {
		return nil, err
	}

	var unhealthy []protocol.UnhealthyPod
	for _, p := range list.Items {
		if p.Status.Phase == "Succeeded" {
			continue
		}
		if isExcluded(p.Metadata.Namespace, p.Metadata.Name, excludes) {
			continue
		}

		total := len(p.Status.ContainerStatuses)
		ready := 0
		restarts := 0
		reason := p.Status.Reason
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Ready {
				ready++
			}
			restarts += cs.RestartCount
			if reason == "" && cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				reason = cs.State.Waiting.Reason
			}
			if reason == "" && cs.State.Terminated != nil && cs.State.Terminated.Reason != "" && cs.State.Terminated.Reason != "Completed" {
				reason = cs.State.Terminated.Reason
			}
			if reason == "" && cs.LastState.Terminated != nil && cs.LastState.Terminated.Reason == "OOMKilled" {
				reason = "OOMKilled"
			}
		}

		notReady := p.Status.Phase != "Running" || (total > 0 && ready < total)
		highRestarts := restarts >= 3 && reason != ""
		if notReady || highRestarts {
			if reason == "" {
				reason = p.Status.Phase
			}
			unhealthy = append(unhealthy, protocol.UnhealthyPod{
				Namespace: p.Metadata.Namespace,
				Name:      p.Metadata.Name,
				Phase:     p.Status.Phase,
				Ready:     fmt.Sprintf("%d/%d", ready, total),
				Restarts:  restarts,
				Reason:    reason,
			})
		}
	}
	return unhealthy, nil
}

// CheckPodsAlive counts Running+Ready pods matching a namespace and name prefix.
func (c *Client) CheckPodsAlive(ctx context.Context, namespace, namePrefix string) (readyPods int, totalPods int, err error) {
	path := "/api/v1/pods"
	if namespace != "" {
		path = fmt.Sprintf("/api/v1/namespaces/%s/pods", namespace)
	}
	var list podList
	if _, err := c.getJSON(ctx, path, &list); err != nil {
		return 0, 0, err
	}
	for _, p := range list.Items {
		if namePrefix != "" && !strings.HasPrefix(p.Metadata.Name, namePrefix) {
			continue
		}
		if p.Status.Phase == "Succeeded" {
			continue
		}
		totalPods++
		if p.Status.Phase != "Running" || len(p.Status.ContainerStatuses) == 0 {
			continue
		}
		allReady := true
		for _, cs := range p.Status.ContainerStatuses {
			if !cs.Ready {
				allReady = false
				break
			}
		}
		if allReady {
			readyPods++
		}
	}
	return readyPods, totalPods, nil
}

// --- cert-manager Certificates ---

type certList struct {
	Items []struct {
		Metadata struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		} `json:"metadata"`
		Status struct {
			NotAfter   string `json:"notAfter"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

// ListExpiringCerts returns cert-manager Certificates that are not Ready or expire within warnDays.
func (c *Client) ListExpiringCerts(ctx context.Context, warnDays int) ([]protocol.ExpiringCert, error) {
	if warnDays <= 0 {
		warnDays = 14
	}
	var list certList
	status, err := c.getJSON(ctx, "/apis/cert-manager.io/v1/certificates", &list)
	if err != nil {
		if status == http.StatusNotFound || status == http.StatusForbidden {
			return nil, nil
		}
		return nil, err
	}

	now := c.now()
	var out []protocol.ExpiringCert
	for _, item := range list.Items {
		ready := false
		for _, cond := range item.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				ready = true
				break
			}
		}
		daysLeft := -1
		var notAfterUnix int64
		if item.Status.NotAfter != "" {
			if t, err := time.Parse(time.RFC3339, item.Status.NotAfter); err == nil {
				notAfterUnix = t.Unix()
				daysLeft = int(t.Sub(now).Hours() / 24)
			}
		}
		if !ready || (notAfterUnix > 0 && daysLeft < warnDays) {
			out = append(out, protocol.ExpiringCert{
				Namespace: item.Metadata.Namespace,
				Name:      item.Metadata.Name,
				Ready:     ready,
				DaysLeft:  daysLeft,
				NotAfter:  notAfterUnix,
			})
		}
	}
	return out, nil
}

// --- ArgoCD Applications ---

type argoAppList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Sync struct {
				Status string `json:"status"`
			} `json:"sync"`
			Health struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"health"`
		} `json:"status"`
	} `json:"items"`
}

// ListArgoCDUnhealthy returns ArgoCD Applications in namespace `argocd` that are OutOfSync or not Healthy.
func (c *Client) ListArgoCDUnhealthy(ctx context.Context) ([]protocol.ArgoCDAppStatus, error) {
	var list argoAppList
	status, err := c.getJSON(ctx, "/apis/argoproj.io/v1alpha1/namespaces/argocd/applications", &list)
	if err != nil {
		if status == http.StatusNotFound || status == http.StatusForbidden {
			return nil, nil
		}
		return nil, err
	}

	var out []protocol.ArgoCDAppStatus
	for _, app := range list.Items {
		syncSt := app.Status.Sync.Status
		healthSt := app.Status.Health.Status
		if syncSt != "Synced" || healthSt != "Healthy" {
			out = append(out, protocol.ArgoCDAppStatus{
				Name:         app.Metadata.Name,
				SyncStatus:   syncSt,
				HealthStatus: healthSt,
				Message:      app.Status.Health.Message,
			})
		}
	}
	return out, nil
}
