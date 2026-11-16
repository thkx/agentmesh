# P5 M53 — Web Asset Consolidation & Observability

**Status:** Implementation in progress. The initial HTTP observability slice is implemented; M53 is not yet accepted.  
**Branch:** `p5/control-plane`  
**Scope:** M53 of [P5 Part 2](p5-part2-operations.md).

## Implemented

### Web asset boundary

- All browser-facing Dashboard and Workflow Editor assets are under repository-root `web/`.
- `web/embed.go` owns the shared embedded filesystem.
- Dashboard Go handlers and Workflow Editor HTTP adapters remain under `internal/transport/http/`.
- Dashboard templates, CSS, HTMX and Workflow Editor source are served from the embedded filesystem.
- Tests verify embedded CSS and Workflow Editor routes.

### HTTP correlation and logging

- Middleware accepts a constrained `X-Request-ID` or generates a cryptographically random identifier.
- Middleware extracts a valid W3C version-00 `traceparent` trace ID or creates a new trace ID.
- Request and trace IDs are attached to request context and returned as response headers.
- Structured request completion logs contain IDs, method, registered route pattern, status and elapsed milliseconds.
- Concrete URL paths, query strings, request bodies, prompts and credentials are not logged by this middleware.

### Metrics

- `GET /metrics` exposes Prometheus text format.
- When `AGENTMESH_API_TOKEN` is configured, metrics access requires the existing Bearer API token. Dashboard cookies do not authorize this endpoint.
- HTTP request count, duration histogram, in-flight requests and process uptime are recorded.
- Model Gateway generate and stream-open operation counts and setup durations are recorded through an observer hook.
- Dashboard adds an Operations page with in-process request/model gateway totals and a pointer to the protected Prometheus scrape endpoint.
- Metric labels are restricted to registered Gin route patterns, a finite HTTP method set, status codes, operation names and outcome names. No model IDs, provider IDs, execution IDs, request IDs, prompts or arbitrary error strings are labels.
- Metrics remain in process memory and are not exported to an external service.

## Current limitations / remaining M53 work

- Trace ID propagation is implemented; OpenTelemetry spans/exporter integration is not yet implemented.
- Model Gateway stream metrics currently measure stream-open/setup latency, not full stream lifetime. HTTP duration covers the full HTTP streaming response.
- Runtime worker and execution-specific gauges are not yet exposed.
- In-app alert evaluation and alert history/rules are not yet implemented. The initial Dashboard Operations metrics summary is implemented.
- No external telemetry or notification delivery is enabled.

These are deliberate remaining deliverables, not accepted features. Do not mark M53 complete until the remaining work and tests are finished.

## Verification

Run:

```bash
bash scripts/verify-p5-m53.sh
```

The script checks shell syntax, Go formatting, build, vet and the complete race-test suite. GitHub Actions also syntax-checks the script.
