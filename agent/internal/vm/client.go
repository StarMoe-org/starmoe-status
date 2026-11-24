package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
)

// Client queries a local VictoriaMetrics instance using the Prometheus instant query API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a VictoriaMetrics query client.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 8 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

type queryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string        `json:"resultType"`
		Result     []querySample `json:"result"`
	} `json:"data"`
	Error string `json:"error,omitempty"`
}

type querySample struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
}

// InstantQuery executes a PromQL instant query against /api/v1/query.
func (c *Client) InstantQuery(ctx context.Context, expr string) ([]querySample, error) {
	u, err := url.Parse(c.baseURL + "/api/v1/query")
	if err != nil {
		return nil, fmt.Errorf("invalid vm base url: %w", err)
	}
	q := u.Query()
	q.Set("query", expr)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vm query status %d", resp.StatusCode)
	}

	var parsed queryResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode vm response: %w", err)
	}
	if parsed.Status != "success" {
		return nil, fmt.Errorf("vm query error: %s", parsed.Error)
	}
	return parsed.Data.Result, nil
}

func sampleFloat(samples []querySample) (float64, bool) {
	if len(samples) == 0 || len(samples[0].Value) < 2 {
		return 0, false
	}
	switch v := samples[0].Value[1].(type) {
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return v, true
	default:
		return 0, false
	}
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// CollectHostMetrics queries CPU, memory, swap, root disk, and optional extra mount metrics.
// Partial failures are tolerated; an error is returned only if all core queries fail.
func (c *Client) CollectHostMetrics(ctx context.Context, extraMountPath, extraMountDevice string) (protocol.HostMetrics, error) {
	var m protocol.HostMetrics
	var errs []string
	succeeded := 0

	queryScalar := func(expr string) (float64, bool) {
		samples, err := c.InstantQuery(ctx, expr)
		if err != nil {
			errs = append(errs, err.Error())
			return 0, false
		}
		val, ok := sampleFloat(samples)
		if ok {
			succeeded++
		}
		return val, ok
	}

	if v, ok := queryScalar(`node_load1`); ok {
		m.CPULoad1 = round2(v)
	}
	if v, ok := queryScalar(`count(node_cpu_seconds_total{mode="idle"})`); ok {
		m.CPUCores = int(math.Round(v))
	}
	if v, ok := queryScalar(`(1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes) * 100`); ok {
		m.MemUsedPct = round2(v)
		m.MemAvailablePct = round2(math.Max(0, 100-v))
	}
	if v, ok := queryScalar(`((node_memory_SwapTotal_bytes - node_memory_SwapFree_bytes) / node_memory_SwapTotal_bytes) * 100`); ok {
		m.SwapUsedPct = round2(v)
	}
	if v, ok := queryScalar(`(1 - node_filesystem_avail_bytes{mountpoint="/",fstype!~"tmpfs|overlay"} / node_filesystem_size_bytes{mountpoint="/",fstype!~"tmpfs|overlay"}) * 100`); ok {
		m.RootDiskPct = round2(v)
	}

	if extraMountPath != "" {
		escPath := strings.ReplaceAll(extraMountPath, `"`, `\"`)
		sizeExpr := fmt.Sprintf(`node_filesystem_size_bytes{mountpoint="%s",fstype!~"tmpfs|overlay"}`, escPath)
		availExpr := fmt.Sprintf(`node_filesystem_avail_bytes{mountpoint="%s",fstype!~"tmpfs|overlay"}`, escPath)

		sizeSamples, sizeErr := c.InstantQuery(ctx, sizeExpr)
		availSamples, availErr := c.InstantQuery(ctx, availExpr)

		device := ""
		if sizeErr == nil && len(sizeSamples) > 0 {
			device = sizeSamples[0].Metric["device"]
		}
		sizeVal, sizeOK := sampleFloat(sizeSamples)
		availVal, availOK := sampleFloat(availSamples)
		if sizeErr == nil && availErr == nil && sizeOK && availOK && sizeVal > 0 {
			m.ExtraDiskPct = round2((1 - availVal/sizeVal) * 100)
			succeeded++
		}

		mountOK := device != ""
		if mountOK && extraMountDevice != "" {
			mountOK = (device == extraMountDevice)
		}
		m.Mounts = append(m.Mounts, protocol.MountCheck{
			Path:           extraMountPath,
			Device:         device,
			ExpectedDevice: extraMountDevice,
			OK:             mountOK,
		})
	}

	if succeeded == 0 && len(errs) > 0 {
		return m, fmt.Errorf("victoria metrics unreachable: %s", errs[0])
	}
	return m, nil
}
