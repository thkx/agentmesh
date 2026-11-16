# P5 Part 2 — Implementation Status

**Branch:** `p5/control-plane`  
**Baseline:** P5 Part 1 / P4 unified execution core  
**Status:** Implementation in progress. This document deliberately does not claim M53–M56 acceptance.

## Delivered in this implementation slice

### M53 — Observability foundation
- Existing bounded-cardinality HTTP and Model Gateway metrics remain the source of process-local counters.
- HTTP request correlation continues to provide request IDs and W3C trace context.
- The HTTP router is wrapped with OpenTelemetry HTTP instrumentation. The default global provider is no-op; no exporter or network telemetry is configured by this change.
- A redacted diagnostics JSON endpoint is exposed at `GET /api/v1/diagnostics/report`, under the existing API-token middleware.
- Existing in-memory alert evaluator and metrics dashboard are retained.

### M54 — Usage ledger
- Additive migration `000044_model_usage_ledger` introduces append-only `model_usage_events` and effective-dated `model_pricing_rates`.
- Successful non-stream Gateway calls and the first terminal usage event for a stream are recorded through a Gateway observer.
- The ledger has a unique idempotency key, provider/model identity, token dimensions, source classification, correlation key, and immutable pricing snapshot.
- Cost arithmetic uses integer micro-currency units. Aggregations keep currencies separate.
- API: `GET /api/v1/usage/summary`, `GET /api/v1/usage/by-model`, `GET /api/v1/usage/rates`, `POST /api/v1/usage/rates`.
- Dashboard: `/dashboard/usage`.

Rate payload example:

```json
{
  "provider_id": "openai",
  "model_id": "gpt-4.1",
  "currency": "USD",
  "input_rate_micros_per_million": 2000000,
  "output_rate_micros_per_million": 8000000
}
```

Rates are expressed as micro-currency units per one million tokens. Historical usage keeps the rate snapshot used at insertion time. An absent rate means cost is unknown, not zero.

### M55 — Encrypted backup primitives and offline CLI
- SQLite snapshots use `VACUUM INTO`, so committed WAL state is included through SQLite snapshot semantics rather than copying a live database file.
- The archive includes the SQLite snapshot, credential encryption key and a checksummed manifest.
- The archive is encrypted with AES-256-GCM; a passphrase is expanded with scrypt (N=32768, r=8, p=1).
- Verification authenticates and checks the archive before extraction.
- `cmd/agentmesh-backup` supports `create`, `verify`, and `restore`. Restore only stages verified files into a new directory; it never overwrites a live database.
- Supply the passphrase through `AGENTMESH_BACKUP_PASSPHRASE` (minimum 12 bytes). It is not persisted by the CLI.

Examples:

```bash
AGENTMESH_BACKUP_PASSPHRASE='use-a-long-unique-passphrase' go run ./cmd/agentmesh-backup create --config configs/config.yaml --output ./backups/agentmesh.backup
AGENTMESH_BACKUP_PASSPHRASE='use-a-long-unique-passphrase' go run ./cmd/agentmesh-backup verify --archive ./backups/agentmesh.backup
AGENTMESH_BACKUP_PASSPHRASE='use-a-long-unique-passphrase' go run ./cmd/agentmesh-backup restore --archive ./backups/agentmesh.backup --destination ./restore-staging
```

Restore staging is intentionally not the same as replacing the active database. Stop AgentMesh and follow a separately reviewed offline replacement procedure.

### M56 — Diagnostics foundation
- The diagnostics report allow-lists metric names, caps log count and line length, sanitizes version strings, and redacts common bearer tokens, secret assignments, URL userinfo/query/fragment and absolute paths.
- The report endpoint does not include prompt/completion content or credentials.

## Remaining before Part 2 can be called complete

- M53: worker/execution-specific gauges; connect the alert evaluator to live dependency/worker/backlog state and render alert history; configurable opt-in OTLP exporter and explicit Model Gateway spans.
- M54: propagate execution/attempt provenance from runtime contexts; expose pagination/time filters and currency-safe cost details in the Dashboard; surface ledger write failures as operational alerts; extend tests to exercise real Gateway streaming and cancellation.
- M55: add signed release metadata verification, platform-aware update checking/staging, and a separately reviewed offline apply procedure; add restore preflight including SQLite integrity/schema validation and pre-restore safety backup.
- M56: complete disk usage, migration and worker health diagnostics; bounded support bundle/log collection; graceful shutdown request integrated with the existing server lifecycle and CLI service status/stop commands.

## Verification

Run:

```bash
bash scripts/verify-p5-part2.sh
```

This runs formatting, build, vet and race tests. GitHub Actions is also configured to check the Part 2 verification script. Local macOS acceptance still needs to be run on the user's machine; CI success is not a substitute for that acceptance.
