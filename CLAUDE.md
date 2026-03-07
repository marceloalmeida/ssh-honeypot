# CLAUDE.md

## Project Overview

SSH-Honeypot is a Go application that poses as an SSH server to capture and analyze attacker behavior. It accepts connections on a configurable port (default 2222), rejects all authentication attempts, and logs connection metadata enriched with geolocation data to InfluxDB. Distributed tracing is exported to Jaeger via OpenTelemetry.

## Repository Structure

```
.
├── ssh-honeypot.go      # Main application: SSH server, handlers, entry point
├── influxdb.go          # InfluxDB write operations (blocking and non-blocking)
├── otel.go              # OpenTelemetry tracer initialization and shutdown
├── ipapi.go             # ip-api.com geolocation integration (free, rate-limited)
├── ipinfo.go            # ipinfo.io geolocation integration (token-based)
├── rsa.go               # RSA host key generation
├── go.mod / go.sum      # Go module dependencies (Go 1.24+)
├── Dockerfile           # Multi-stage build: golang:1.25-alpine → distroless
├── docker-compose.yaml  # Full stack: honeypot + InfluxDB + Jaeger
├── assets/              # Grafana dashboard JSON and screenshot
└── .github/
    ├── workflows/
    │   ├── go.yml       # CI: build + test on push/PR to main
    │   └── ghcr.yml     # Publish Docker image to GHCR on tag push
    └── dependabot.yml   # Daily dependency updates (Go, Actions, Docker)
```

All Go source is in a single `main` package at the repository root. There are no sub-packages.

## Build & Test Commands

```bash
# Build
go build -v ./...

# Run tests
go test -v ./...

# Run locally (requires environment variables — see below)
go run .

# Docker
docker compose up --build
```

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `INFLUXDB_URL` | Yes | — | InfluxDB HTTP endpoint |
| `INFLUXDB_TOKEN` | Yes | — | InfluxDB auth token |
| `INFLUXDB_ORG` | Yes | — | InfluxDB organization |
| `INFLUXDB_BUCKET` | Yes | — | InfluxDB bucket name |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | No | `localhost:4317` | OTLP/gRPC collector endpoint |
| `OTEL_SERVICE_NAME` | No | `ssh-honeypot` | OpenTelemetry service name |
| `IPINFOIO_TOKEN` | No | — | ipinfo.io token (falls back to ip-api.com) |
| `HOST_KEY_PATH` | No | `./host_key` | Path to SSH host private key (auto-generated) |
| `SSH_PORT` | No | `2222` | SSH listening port |
| `INFLUXDB_WRITE_PRIVATE_IPS` | No | `false` | Log connections from private IPs |
| `INFLUXDB_NON_BLOCKING_WRITES` | No | `false` | Use async InfluxDB writes |

## CI/CD

- **go.yml**: Runs `go build` and `go test` on push/PR to `main` (Ubuntu, Go 1.24).
- **ghcr.yml**: Builds multi-platform Docker images (amd64, arm64) and publishes to `ghcr.io` on tag push.
- **Dependabot**: Daily checks for Go modules, GitHub Actions, and Docker base image updates.

## Code Conventions

- **Single package**: Everything is in `package main` at the root.
- **No test files currently exist** — but the CI runs `go test -v ./...`, so tests should use standard Go testing (`*_test.go`).
- **OpenTelemetry tracing**: Wrap significant operations in `tracer.Start()` spans. Record errors with `span.RecordError()` and set status with `span.SetStatus()`. Propagate context through function parameters.
- **Error handling**: Return errors up the call stack. Errors within traced operations should be recorded on the span before returning.
- **Geolocation strategy**: If `IPINFOIO_TOKEN` is set, use ipinfo.io; otherwise use ip-api.com with exponential backoff for rate limits.
- **Exponential backoff**: Uses `github.com/cenkalti/backoff/v4` for retry logic on rate-limited APIs.
- **In-memory caching**: Uses `github.com/patrickmn/go-cache` (5-minute TTL) for rate limit state.

## Key Dependencies

- `github.com/gliderlabs/ssh` — SSH server library
- `github.com/influxdata/influxdb-client-go/v2` — InfluxDB v2 client
- `go.opentelemetry.io/otel` — OpenTelemetry tracing
- `github.com/cenkalti/backoff/v4` — Exponential backoff
- `github.com/patrickmn/go-cache` — In-memory cache
- `golang.org/x/crypto` — SSH key cryptography

## Architecture Notes

- The SSH server spoofs version string `SSH-2.0-OpenSSH_7.4`.
- Connections get a 30-second deadline and 10-second idle timeout.
- Both password and public key authentication handlers capture credentials but always reject.
- The session handler sends a fake "Permission denied" message and holds the connection open until timeout.
- Host keys are auto-generated (4096-bit RSA) on first run if not found at `HOST_KEY_PATH`.
