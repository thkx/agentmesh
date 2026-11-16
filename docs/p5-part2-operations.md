# P5 Part 2 — Operations & Observability (M53–M56)

**Status:** Architecture approved for implementation; no Part 2 code is claimed complete by this document.  
**Branch:** `p5/control-plane`  
**Baseline:** P5 Part 1, commit `733f1224664bbb3b2e39f76ced4b310a74da65f0` (locally accepted on Go 1.27.3 darwin/arm64 and verified by GitHub Actions).  
**Product direction:** Local-first Operations & Observability Console. Go `html/template` + HTMX + small amounts of vanilla JavaScript.

## 1. Goal

P5 Part 2 makes AgentMesh observable, measurable, diagnosable and safely maintainable as a running local-first product. It adds operational capabilities around the existing runtime; it does not create another runtime, scheduler, queue, execution state machine or event ledger.

The Part 2 console adds:
- system and runtime metrics, request correlation and optional distributed tracing;
- durable model token usage and estimated cost accounting;
- safe database backup, verification and offline restore;
- verified application update staging and controlled application;
- diagnostics, health checks and graceful service shutdown.

## 2. Repository and frontend boundary

All browser-facing source assets belong under the repository-root `web/` directory. This is a firm repository convention, not an optional location.

```text
web/
  embed.go
  dashboard/
    templates/
      layouts/
      components/
      pages/
      partials/
    static/
      css/
      js/
      vendor/
        htmx.min.js
  workflow-editor/
    index.html
    app.js

internal/
  transport/
    http/
      dashboard/       # Go handlers, route registration, view models and query/command adapters
      editor.go        # Workflow Editor HTTP adapter only
  observability/       # metrics, trace/correlation and alert evaluation
  usage/               # usage ledger, pricing and aggregation
  operations/
    backup/            # consistent snapshot, manifest, encryption and verification
    upgrade/           # release metadata, signature verification and staging
    diagnostics/       # redacted diagnostic collection
```

Rules:
- `web/embed.go` owns the single `embed.FS` for Dashboard and Workflow Editor source assets.
- `internal/transport/http/dashboard` contains Go code only; no templates, CSS, JavaScript or vendored browser libraries.
- HTTP handlers consume embedded assets through the `web` package. The embed package must not import `internal` packages.
- Remove duplicate Workflow Editor asset copies after route tests prove the new embedded paths work.
- No Node, npm, bundler, CDN or frontend SPA is introduced.

## 3. Architecture

```text
Browser
  |
  | /dashboard/* (session + same-origin protections)
  v
Dashboard HTTP adapters
  |                 \
  |                  +---- Usage / Operations / Diagnostics application services
  |                                  |
  v                                  v
Existing application services     Existing SQLite + filesystem
  |
  +---- Agent / Coding / Workflow / CLI runtimes
  +---- Execution Manager and P4 event/provenance read models
  +---- Model Gateway
             |
             +---- Usage recorder (one record per completed provider request)
             +---- Metrics and trace instrumentation
```

The Model Gateway remains the common instrumentation boundary for model usage. Runtime-specific code must not independently guess token counts or write duplicate usage records. Execution/Attempt linkage is optional metadata propagated through request context; it must never be inferred from prompt contents.

### Cross-cutting invariants

1. Local-first: no telemetry or usage data leaves the machine unless the operator explicitly configures an exporter.
2. No prompt, completion, tool arguments, API key, credential, Authorization header, database DSN or raw provider error is recorded in metrics, spans, alert labels or diagnostic bundles.
3. Metrics labels are bounded. Never use Execution IDs, Attempt IDs, user text, request IDs or arbitrary error strings as metric labels.
4. Token usage distinguishes provider-reported values from estimates. Unknown usage is not silently treated as zero.
5. Cost is an estimate based on operator-configured rates. Historical usage retains the exact pricing snapshot used for its estimate.
6. Backups include the SQLite database and the credential-encryption key material required to decrypt stored credentials. Backup archives are encrypted; plaintext secrets are never written to an unprotected archive.
7. Restore and binary replacement are offline operations. The running process must not overwrite its own database or executable.
8. Every destructive operation is explicit, audited, bounded and recoverable where technically possible.
9. Preserve API/CLI contracts and P3/P4 persistence semantics. No new queue, scheduler, Execution Manager, event ledger or duplicate runtime store.
10. No public-internet deployment claim is made by Part 2. Existing loopback default and authentication boundary remain.

## 4. Milestones and deliverables

### M53 — Web Asset Consolidation & Observability Foundation

**Deliverables**
- Move Dashboard templates and static assets to `web/dashboard/`; move/normalize Workflow Editor source under `web/workflow-editor/`.
- Add `web/embed.go` and route all embedded assets through its read-only `embed.FS`.
- Keep Dashboard and Workflow Editor Go handlers under `internal/transport/http/`.
- Add request ID / correlation ID middleware and include the ID in structured request logs and safe error responses.
- Add bounded HTTP, runtime-worker, execution and Model Gateway metrics.
- Expose a Prometheus-compatible metrics endpoint protected by the existing API-token boundary when a token is configured.
- Add OpenTelemetry-compatible spans for HTTP and Model Gateway operations. Export is disabled by default; an explicitly configured local/OTLP exporter is opt-in.
- Add an in-app alert evaluator for a small set of actionable conditions (database unavailable, worker stopped, repeated provider failures, execution reconciliation backlog). No external notification integrations in this milestone.

**Acceptance**
- All front-end source files are under root `web/`; no frontend source remains under `internal/`.
- Dashboard and Workflow Editor render and serve assets from a normal compiled binary with no external network.
- Request IDs remain stable through a request and appear in structured logs without exposing request payloads.
- Metrics use bounded labels and do not contain secrets or high-cardinality IDs.
- Tracing produces no outbound traffic unless explicitly enabled.
- Existing Dashboard/API auth and CSRF tests continue to pass.

### M54 — Model Usage & Cost Accounting

**Deliverables**
- Add a durable append-only model usage ledger at the Model Gateway boundary.
- Record provider/model, request timestamp, input/output/total tokens, cached/reasoning tokens when available, usage source, request/trace correlation and optional Execution/Attempt provenance.
- Handle both non-streaming and streaming calls. A streamed request is recorded once, not once per usage delta and again at completion.
- Distinguish provider-reported usage, locally estimated usage and unavailable usage.
- Add operator-managed per-model pricing rates with currency and effective time.
- Snapshot rates on each usage record and calculate estimated cost using integer arithmetic; do not use floating point for persisted money values.
- Add usage overview, per-provider/model breakdown, time filters, pagination and Execution detail links where provenance exists.
- Add retention/export controls only after data-loss behavior and backup interactions are tested.

**Schema decision**
- A new additive migration is justified because durable usage accounting is a concrete product requirement not represented by the current Execution/Event model.
- The migration adds an append-only `model_usage_events` ledger and pricing records (or an equivalent versioned pricing table). It does not modify P3/P4 Execution lifecycle tables.
- Each usage event stores a pricing snapshot so later rate edits cannot rewrite historical cost estimates.
- Add indexes for timestamp, provider/model and optional Execution/Attempt references. Add idempotency protection for a single gateway request.

**Acceptance**
- Every successful Gateway request with provider usage is recorded exactly once.
- Missing provider usage is clearly marked; estimates are never represented as provider facts.
- Streaming, cancellation, provider errors and duplicate completion events have explicit tested semantics.
- Historical costs remain unchanged after pricing edits.
- Dashboard totals reconcile with direct SQL aggregation of the ledger.
- No prompt/completion or credential material is stored in the usage ledger.

### M55 — Backup, Restore & Upgrade Workflow

**Deliverables**
- Create a consistent SQLite snapshot while the application is running, including WAL state through SQLite-supported snapshot/backup semantics rather than copying the live database file.
- Package the database, required credential-encryption key material and a manifest containing application version, schema version, creation time and checksums.
- Encrypt backup archives with a user-supplied passphrase; use a reviewed password KDF and authenticated encryption. The passphrase is write-only and is never persisted or logged.
- Provide backup list, download, integrity verification and retention controls.
- Add an offline CLI restore operation. It refuses to run while the database is in use, verifies the archive and schema, creates a pre-restore safety backup, and only then replaces the database/key pair.
- Add update check and stage operations using official release metadata and cryptographic signature/checksum verification.
- Stage updates without replacing the running executable. Applying an update is an explicit offline CLI operation after a verified backup.

**Acceptance**
- Backup taken during active writes restores to a transactionally consistent database.
- Backup integrity and passphrase failures are detected before touching the active database.
- Restore refuses unsafe in-process replacement and never deletes the only valid backup.
- Credential decryption works after restore using the bundled key material.
- Update artifacts are rejected if signatures/checksums or platform metadata do not match.
- No web action executes an unverified binary or overwrites the running executable.

### M56 — Diagnostics & Service Lifecycle

**Deliverables**
- Operations dashboard for process uptime/version, readiness dependencies, SQLite health/integrity, migration version, worker status, execution reconciliation backlog, disk usage and recent operational alerts.
- Diagnostic report generation with strict redaction of credentials, API tokens, provider URLs containing userinfo/query strings, prompts, completions and sensitive file paths.
- Downloadable support bundle containing safe configuration metadata, health checks, bounded recent logs and selected metrics.
- Explicit graceful-shutdown action through the existing application shutdown path, with confirmation and visible shutdown-requested state.
- CLI start/status/stop guidance and service-manager integration points.
- Restart/start buttons are displayed only when a supported external supervisor (such as a configured service manager) can actually perform the operation. The application must not claim it can restart itself.

**Acceptance**
- Diagnostic bundles pass automated secret-redaction tests using seeded canary secrets.
- Health checks distinguish liveness from readiness and identify failed dependencies without leaking DSNs.
- Worker status is based on observed worker heartbeats/state, not guessed from configuration.
- Graceful shutdown stops producers, waits for in-flight work and closes the database using the existing Container lifecycle.
- Unsupported start/restart actions are not rendered.
- Backup, update, diagnostics and shutdown operations require explicit confirmation and are covered by integration tests.

## 5. Proposed dashboard navigation

- Overview
- Providers
- Models
- Agents
- Workflows
- Executions
- Usage & Costs
- Operations
  - Metrics & Alerts
  - Backups
  - Updates
  - Diagnostics

Use existing Dashboard session protections. Metrics scrape endpoints remain under API-token protection; browser cookies must not become API credentials.

## 6. Database and dependency policy

- Preserve SQLite + `modernc.org/sqlite`, golang-migrate and sqlc.
- No Redis, Kafka, GORM, Asynq or mandatory cloud service.
- One additive usage/pricing migration is expected. Backup manifests and staged release metadata live in the filesystem, not in new database tables unless an implementation audit demonstrates a concrete need.
- Prefer small, maintained libraries with a clear purpose. OpenTelemetry exporters are opt-in. Do not add a metrics or tracing SaaS dependency.
- Every migration includes up/down scripts, sqlc regeneration and migration tests.
- Do not alter accepted P3/P4 tables or lifecycle contracts to store usage data.

## 7. Verification and release gate

Add `scripts/verify-p5-part2.sh`. It must check (without auto-formatting source):
- shell syntax and Go formatting;
- `sqlc generate` consistency where applicable;
- migration up/down and backup/restore integration tests;
- `go build ./...`;
- `go vet ./...`;
- `go test -race ./...`;
- observability cardinality/redaction tests;
- usage accounting and streaming idempotency tests;
- backup encryption, integrity and restore tests;
- upgrade signature/staging safety tests;
- Dashboard and CLI operational workflow tests.

The final report must name the exact branch and commit SHA, CI run for that exact SHA, local Go 1.27.3 darwin/arm64 result, and any checks intentionally skipped with reasons.

## 8. Explicitly deferred to P5 Part 3 or later

- Fine-grained API tokens, scoped RBAC, token rotation and remote-access hardening.
- Public internet exposure, multi-user tenancy and cloud control plane.
- Multi-machine Runner and distributed scheduling.
- External alert delivery (email, Slack, webhook) and hosted telemetry.
- Automatic in-process database restore or self-replacement of the running binary.
