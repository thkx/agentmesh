# P5 Part 2 — Operations & Observability

**Branch:** `p5/control-plane`  
**Baseline:** P5 Part 1 / P4 unified execution core  
**Implementation status:** M53–M56 implementation delivered; final GitHub Actions and local macOS acceptance are tracked separately.

Part 2 adds operational capabilities around the existing runtime. It does not introduce a second scheduler, queue, execution state machine, or event ledger.

## M53 — Web assets, metrics, tracing and alerts

- Browser-facing templates, styles, HTMX and Workflow Editor assets remain under repository-root `web/` and are embedded into the binary.
- HTTP request metrics use registered route templates, bounded method/status labels, duration histograms and in-flight gauges.
- Model Gateway metrics count generate, stream-open and stream-completion outcomes. Stream duration and terminal errors are observed without recording raw provider errors.
- Runtime scheduler, workflow, reconciler, collaboration dispatcher and wakeup worker loops update the live worker gauge on start/exit.
- Execution active/backlog gauges are refreshed by the metrics scrape and Operations API.
- Usage ledger write failures are counted.
- Request ID and W3C trace correlation remain enabled. HTTP and Model Gateway operations create OpenTelemetry spans; span attributes contain only provider/model identifiers, not prompts, completions, credentials or arbitrary errors.
- Optional OTLP/HTTP export is disabled by default. Enable it with:

```yaml
tracing:
  enabled: true
  otlp_endpoint: "http://127.0.0.1:4318"
  headers_env: AGENTMESH_OTEL_EXPORTER_OTLP_HEADERS
```

Remote collectors must use HTTPS. Plain HTTP is accepted only for loopback endpoints. Optional headers are read from the named environment variable, never logged or persisted.

- The fixed alert evaluator tracks database availability, stopped workers, repeated provider failures, reconciliation backlog and usage-ledger write failures. Active and resolved alert history is retained in process memory and shown in Dashboard Operations.
- API: `GET /metrics`, `GET /api/v1/operations/alerts`, `GET /api/v1/operations/health`.

Alert history is intentionally process-local; it is not a durable incident-management system and no external notifications are sent.

## M54 — Usage and cost ledger

- Append-only `model_usage_events` with idempotency key, provider/model, token dimensions, source classification, correlation and optional execution/attempt provenance.
- Effective-dated pricing rates are snapshotted at record time. Cost uses integer micro-currency arithmetic; aggregations keep currencies separate.
- Non-stream Gateway calls and the first terminal usage event from a stream are recorded once.
- Agent scheduler context and Workflow Worker context explicitly propagate managed Execution/Attempt identity; no provenance is inferred from prompt content.
- Ledger write failures are visible in metrics and trigger a critical Operations alert.
- API:
  - `GET /api/v1/usage/summary`
  - `GET /api/v1/usage/by-model`
  - `GET /api/v1/usage/events` (RFC3339 time filters, provider/model/execution/attempt filters, bounded pagination)
  - `GET /api/v1/usage/rates`
  - `POST /api/v1/usage/rates`
- Dashboard `/dashboard/usage` provides time-range filters, per-model/currency breakdown, paginated request ledger and links to managed Execution detail when provenance exists.

Rates are operator-supplied estimates, not provider invoices. Missing provider usage is recorded as unavailable; unknown cost is not represented as zero.

## M55 — Backup, restore and signed upgrades

### Backup / restore

- SQLite backup uses `VACUUM INTO` for a consistent snapshot.
- The archive includes the database, credential encryption key and manifest, encrypted using AES-256-GCM with a scrypt-derived key.
- Verify authenticates the archive, checks member hashes, runs SQLite `integrity_check` and checks the migration version when present.
- Restore first stages verified files into a new directory. `apply-restore` re-verifies the original encrypted archive, refuses to run without `--confirm-stopped`, creates pre-restore copies of both database and credential key, and then replaces the files offline.
- The service never overwrites its own live database from an HTTP request.

Examples:

```bash
export AGENTMESH_BACKUP_PASSPHRASE='use-a-long-unique-passphrase'
go run ./cmd/agentmesh-backup create --config configs/config.yaml --output ./backups/agentmesh.backup
go run ./cmd/agentmesh-backup verify --archive ./backups/agentmesh.backup
go run ./cmd/agentmesh-backup restore --archive ./backups/agentmesh.backup --destination ./restore-staging
# Stop AgentMesh before this explicit offline replacement:
go run ./cmd/agentmesh-backup apply-restore --archive ./backups/agentmesh.backup --database ./data/agentmesh.db --credential-key ./data/keys/credential.key --backup-dir ./backups/pre-restore --confirm-stopped
```

The database and credential-key paths must match the configured local deployment. Keep the pre-restore backup directory protected; it contains sensitive key material.

### Upgrade

- Release metadata is Ed25519-signed. The manifest binds version, OS/architecture, binary size and SHA-256.
- `agentmesh-upgrade check` downloads only signed metadata over HTTPS (loopback HTTP is allowed for local testing).
- `stage` verifies signature, platform, size and digest before copying the binary into a new staging directory.
- `verify` rechecks the staged package.
- `apply` requires `--confirm-stopped`, atomically replaces an existing executable and preserves a timestamped rollback copy.
- The trusted Ed25519 public key is operator-managed and supplied as a raw 32-byte key file. No key is embedded in this repository; operators must obtain the trusted key through an authenticated release channel.

Example:

```bash
go run ./cmd/agentmesh-upgrade check --manifest-url https://releases.example/agentmesh.json --signature-url https://releases.example/agentmesh.json.sig --public-key ./trusted-release.pub
go run ./cmd/agentmesh-upgrade stage --manifest ./agentmesh.json --signature ./agentmesh.json.sig --binary ./agentmesh --public-key ./trusted-release.pub --output ./upgrade-staging
go run ./cmd/agentmesh-upgrade verify --stage ./upgrade-staging --public-key ./trusted-release.pub
# Stop AgentMesh before this explicit offline replacement:
go run ./cmd/agentmesh-upgrade apply --stage ./upgrade-staging --public-key ./trusted-release.pub --target ./bin/agentmesh --backup-dir ./backups/upgrades --confirm-stopped
```

Part 2 does not publish releases or manage signing-key rotation; it verifies and applies packages from an operator-selected release source.

## M56 — Diagnostics and service operations

- `GET /api/v1/diagnostics/report` returns an allow-listed, redacted report.
- `GET /api/v1/diagnostics/bundle` downloads a bounded ZIP containing diagnostics JSON, Prometheus metrics and health JSON.
- A bounded in-memory log ring retains only static log message text; structured attributes are excluded before report redaction.
- `GET /api/v1/operations/health` exposes database, worker, execution, provider and ledger health without returning raw dependency errors.
- `POST /api/v1/operations/shutdown` requests graceful process shutdown. It is protected by the existing Bearer API-token boundary when configured.
- `cmd/agentmesh-ops status` and `cmd/agentmesh-ops shutdown` provide local command-line controls. The CLI reads `AGENTMESH_API_TOKEN` from the environment and refuses to send it over remote plaintext HTTP.

## Security and operational boundaries

- No prompts, completions, tool arguments, API keys, Authorization values, raw provider errors or DSNs are included in metrics, spans or support bundles.
- Metrics labels are bounded and exclude request/execution/attempt IDs.
- Telemetry export is disabled unless explicitly configured.
- Backup/restore and executable replacement are offline operations with explicit stop confirmation and rollback copies.
- The default local-first deployment and existing API authentication behavior are unchanged.

## Verification

Run on the development machine:

```bash
bash scripts/verify-p5-part2.sh
```

This performs shell syntax, gofmt, build, vet and race tests. GitHub Actions performs the same Go checks on the branch. CI success does not replace the final local macOS acceptance run.
