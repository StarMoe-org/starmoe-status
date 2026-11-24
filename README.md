# StarMoe Status

`status.star.moe` is StarMoe's standalone status page, full-stack health monitor, and alerting hub. It runs on an
isolated AWS Lightsail host, completely separate from the DE, JP1, JP2, and US service clusters.

Design: [docs/design.md](docs/design.md).

## What it does

- **Public status page** (`/`, `/incidents`): real-time availability, 90-day uptime bars, 24h latency charts, dual-line
  (CN EdgeOne / Global Cloudflare) visibility, Sekai five-region summary, incident & maintenance timelines, and RSS feed.
- **Internal ops console** (`/admin`, protected by Cloudflare Access): aggregated CPU/memory/disk watermarks across all
  4 k3s clusters, unhealthy pods, ArgoCD sync status, expiring cert-manager certificates, Sekai session details,
  masterdata sync freshness, and incident/maintenance management.
- **Dual-track monitoring**:
  - *Track A (Pull)*: concurrent HTTP/HTTPS, TLS expiry, and TCP probes from Lightsail with debounce state transitions.
  - *Track B (Push)*: lightweight read-only `starmoe-status-agent` on each k3s cluster reporting sanitized heartbeats
    every 60s, with a 3-minute Dead-Man's-Switch and topology-aware alert suppression.
- **Alerting**: authenticated SSE stream (`/api/bot/stream`) + cursor catch-up for the `moebot` QQ plugin, plus optional
  email fallback via Resend or SMTPS.

## Layout

```text
pkg/protocol/   Shared Go types for heartbeats, alerts, and public API payloads.
server/         Lightsail Go server (net/http, pure-Go SQLite WAL, embedded web/out).
agent/          Lightweight read-only k3s cluster agent (VictoriaMetrics, k8s API, deep checks).
web/            Next.js static export (React 19, Tailwind CSS 4).
config/         Declarative monitor definitions (monitors.example.yaml).
deploy/         Lightsail (Caddy, systemd, backup) and ArgoCD k8s manifests (no secret values).
docs/           Architecture and design documents.
```

## Development

```bash
# Run all Go unit tests
go test -race ./...

# Build server and agent binaries (Linux amd64, zero CGO)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/starmoe-status ./server/cmd/starmoe-status
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/starmoe-status-agent ./agent/cmd/status-agent

# Build static frontend
cd web && npm ci && npm run build   # writes web/out
```

The server reads its configuration from environment variables (`deploy/lightsail/env.example`) and a monitor YAML file
(`config/monitors.example.yaml`). The cluster agent reads its configuration from environment variables
(`deploy/k8s/status-agent/secret.env.example`).

## Endpoints

| Path | Who | |
|---|---|---|
| `GET /healthz` | probes | process alive & SQLite writable |
| `GET /api/v1/summary` | public | sanitized public monitor states, node summaries, active incidents (30s cache) |
| `GET /api/v1/monitors/{id}/history` | public | 90-day daily uptime & 24h latency for public monitors |
| `GET /api/v1/incidents`, `GET /rss.xml` | public | public incident/maintenance feed |
| `POST /api/ingest/heartbeat` | cluster agents | per-cluster Bearer token |
| `GET /api/bot/stream`, `/api/bot/events`, `/api/bot/summary` | moebot plugin | Bot Bearer token (SSE + catch-up + query) |
| `GET /api/admin/overview`, `POST/PATCH /api/admin/incidents` | ops admins | Cloudflare Access JWT (`Cf-Access-Jwt-Assertion`) |

## Deployment

- **Server**: single static Go binary with embedded `web/out`, managed by `systemd` behind `Caddy` on AWS Lightsail
  (see `deploy/lightsail/`).
- **Agent**: GitHub Actions builds and publishes multi-arch images to `ghcr.io/starmoe-org/starmoe-status-agent` on
  every push to `main`; ArgoCD deploys `deploy/k8s/status-agent/` to all 4 clusters with read-only RBAC.

This repository is public. Never commit secrets, tokens, server IPs, private hostnames, or user data; CI runs `gitleaks`.
