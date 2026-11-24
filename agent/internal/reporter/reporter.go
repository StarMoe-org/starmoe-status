package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/StarMoe-org/starmoe-status/pkg/protocol"
)

// Reporter pushes HeartbeatPayload snapshots to the central status server with exponential backoff.
type Reporter struct {
	ingestURL   string
	token       string
	userAgent   string
	httpClient  *http.Client
	maxAttempts int
	baseBackoff time.Duration
}

// New creates a heartbeat Reporter.
func New(ingestURL, token, version string, httpClient *http.Client) *Reporter {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	ua := "starmoe-status-agent"
	if version != "" {
		ua += "/" + version
	}
	return &Reporter{
		ingestURL:   ingestURL,
		token:       token,
		userAgent:   ua,
		httpClient:  httpClient,
		maxAttempts: 3,
		baseBackoff: time.Second,
	}
}

// Push sends the heartbeat payload to /api/ingest/heartbeat, retrying transient failures.
func (r *Reporter) Push(ctx context.Context, payload protocol.HeartbeatPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal heartbeat: %w", err)
	}

	var lastErr error
	backoff := r.baseBackoff

	for attempt := 1; attempt <= r.maxAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.ingestURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+r.token)
		req.Header.Set("User-Agent", r.userAgent)

		resp, err := r.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}

		lastErr = fmt.Errorf("ingest status %d: %s", resp.StatusCode, string(bytes.TrimSpace(respBody)))
		// Do not retry permanent client errors (e.g. invalid token or malformed payload).
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return lastErr
		}
	}

	return lastErr
}
