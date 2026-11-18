# P5 Product-Level End-to-End Acceptance

**Target branch:** `p5/control-plane`  
**Baseline:** Part 1 and Part 2 accepted; start from the current branch head.  
**Purpose:** Validate the running product across HTTP, Dashboard, provider execution, usage accounting, backup/restore and operations. Unit/race tests alone do not satisfy this gate.

## 1. Automated smoke test

Start AgentMesh using the same config and database you intend to validate. Keep it bound to loopback unless remote access has been separately hardened.

The API token must be configured in the running AgentMesh service. To enable the Bearer-authenticated acceptance gate without environment variables, add the following to the `server:` section of your config:

```yaml
server:
  api_token_file: ./data/api-token
```

Create the token file (the token must be at least 32 characters). These commands prompt without echo and create a file readable only by your account:

```bash
mkdir -p data
umask 077
read -rsp "New AgentMesh API token: " token; echo
printf '%s\n' "$token" > data/api-token
chmod 600 data/api-token
unset token
```

Restart AgentMesh after changing the config. The service rejects symlinks, non-regular files, files accessible by group/other users, and tokens shorter than 32 characters. Existing `AGENTMESH_API_TOKEN` remains supported and takes precedence for automated deployments.

The backup passphrase is a separate password used only to encrypt this test backup. Run the script directly: it prompts for both values with terminal echo disabled. Neither value needs to be exported or added to shell history.

```bash
bash scripts/accept-p5-e2e.sh
```

The backup CLI accepts its passphrase from standard input, so the smoke test does not export `AGENTMESH_BACKUP_PASSPHRASE`. The API token is held in the script process and used for authenticated requests. Optional overrides remain available for automation:
- `AGENTMESH_BASE_URL` (default: `http://127.0.0.1:8080`)
- `AGENTMESH_CONFIG` (default: `configs/config.yaml`)



The script is non-destructive. It checks:
- `/healthz` and `/readyz`;
- Bearer authentication and operational health;
- Dashboard login and primary navigation pages;
- redacted diagnostics report, ZIP support bundle and Prometheus metrics;
- Usage summary read models;
- encrypted backup creation, archive verification and isolated restore staging.

It deliberately does not call a paid model, create Agent/Workflow runs, overwrite the active database, or shut down the service.

## 2. Real provider and model call

1. Open `/dashboard/providers` and verify the configured provider's type, endpoint and enabled state.
2. Add or update its credential using the Dashboard credential form. Confirm the UI only shows configured/not-configured state; it must never reveal the secret again.
3. Open `/dashboard/models` and verify that the model references the intended provider and uses the upstream model identifier expected by that provider.
4. Use the provider connectivity test. A successful result must be an actual upstream request, not merely a saved configuration.
5. Run one small, non-sensitive prompt through an Agent or Workflow configured to use that model. Record the execution ID and the provider/model used. Avoid shell, filesystem or network tools for this first run.

Pass criteria:
- The execution reaches a terminal success state.
- Execution detail and Timeline show the lifecycle and attempt provenance.
- The result is visible in the expected run/detail view.
- The Usage page and `GET /api/v1/usage/summary` reflect the call. Token counts may be unavailable for providers that do not return usage; unknown pricing must remain unknown, not silently become zero.
- The execution/request correlation is traceable without exposing prompt contents or credentials in logs.

## 3. Workflow orchestration

1. Create or import a minimal two-node workflow using the Dashboard Workflow editor. Use a first node that produces a short text result and a second node that consumes it; both should use the same known-good model initially.
2. Save the workflow and reload it. Confirm the definition survives a reload and the graph/YAML representations remain consistent.
3. Start one run and observe its state in the Workflow page and Unified Execution Center.
4. Inspect the run history, node observations, execution Timeline and artifacts/evidence.
5. If the workflow supports approval, test an approval pause/resume separately. Do not simulate recovery against a successful run.

Pass criteria:
- One canonical run/execution is visible; no duplicate state is created by the Dashboard.
- Node transitions and evidence have stable provenance.
- The run reaches the expected terminal state and usage is recorded once per terminal model usage event.

## 4. Backup and restore drill

The automated script verifies and extracts a backup into a new temporary directory. For the operational drill:

1. Create a named backup outside the repository and outside the live database directory.
2. Run `agentmesh-backup verify` and retain the manifest output.
3. Restore to a new empty directory and verify the extracted SQLite database with the provided integrity checks.
4. Inspect that the credential encryption key is included in the encrypted archive. Treat the archive and passphrase as highly sensitive.
5. For an actual replacement drill, stop AgentMesh first, take an independent copy of the active DB and key, and follow `docs/p5-part2-operations.md`. Never test `apply-restore` against the only copy of a live database.

Pass criteria:
- Backup verifies and restore staging succeeds.
- The restored database passes SQLite integrity and migration checks.
- The credential key and database belong to the same snapshot.
- The active database remains untouched during the non-destructive test.

## 5. Diagnostics and graceful shutdown

1. Open `/dashboard/operations/metrics`.
2. Inspect `GET /api/v1/operations/health`, `GET /api/v1/diagnostics/report` and `GET /api/v1/diagnostics/bundle`.
3. Confirm the report contains no API token, provider key, prompt, URL query string, or local absolute path.
4. Run the operations CLI without exporting the token:
   ```bash
   read -rsp "AgentMesh API token: " token; echo
   printf '%s' "$token" | go run ./cmd/agentmesh-ops status
   unset token
   ```
   The CLI reads the token from stdin when the environment override is absent.
5. When ready to stop the service, use the Dashboard shutdown confirmation or `go run ./cmd/agentmesh-ops shutdown`; verify the process exits cleanly and its database can be reopened.

## 6. Automated test coverage

Run the complete local/CI suite with:

```bash
bash scripts/verify-p5-all.sh
```

This runs shell validation, frontend asset-boundary checks, formatting, SQLC consistency when the binary is installed, the complete Go suite under the race detector, build and vet. To include the running-instance smoke test, set `AGENTMESH_RUN_PRODUCT_SMOKE=1`; the script then prompts for the API token and backup passphrase without echo.

P5-specific test coverage includes:

| Area | Automated coverage |
|---|---|
| Provider/model catalog and credential secrecy | HTTP catalog lifecycle, concurrent catalog writes, credential response redaction |
| Model Gateway | Generate and streaming adapters, provider/model normalization, error normalization, cancellation and observer behavior |
| Usage | Idempotency, immutable pricing snapshots, currency-separated aggregates, HTTP summary read models |
| Workflow engine | Sequential/parallel execution, queued worker, recovery, approval continuation, conditions and fan-out |
| Workflow authoring | Two-agent-node YAML/graph round-trip, HTTP JSON/YAML/graph synchronization, malformed graph/YAML rejection |
| Execution and provenance | P4 lifecycle, event ledger, unified execution and timeline handler tests |
| Diagnostics and observability | Redaction, allow-listed metrics, report bounds, support ZIP contents, authenticated metrics endpoint |
| Backup and restore | Encryption, wrong-passphrase rejection, archive verification, SQLite integrity, isolated extraction and offline apply safeguards |
| Upgrade | Ed25519 signature verification, checksum rejection, staging and offline apply safeguards |
| Service lifecycle | Shutdown closes the database and the persisted database can be reopened |

The automated suite uses deterministic test adapters and isolated temporary SQLite databases. It does not spend money or require a third-party provider key. Real provider connectivity and one real Agent/Workflow invocation remain explicit live-environment acceptance gates.

## 7. Acceptance record


Do not mark P5 product-level acceptance complete until all rows are backed by observed evidence.

| Gate | Evidence | Status |
|---|---|---|
| Automated runtime smoke | User-run `accept-p5-e2e.sh` output (2026-11-18) | PASS |
| Dashboard navigation | User-run authenticated page checks (2026-11-18) | PASS |
| Real provider connectivity | Successful provider test | Pending |
| Real Agent/Workflow run | Execution ID + terminal status | Pending |
| Timeline and evidence | P4 handler/event-ledger unit and integration coverage; live run inspection | Pending live gate |
| Usage accounting | Before/after usage snapshot | Pending |
| Backup and restore | User-run encrypted archive verification + isolated restore staging (2026-11-18) | PASS |
| Diagnostics redaction | Report, support ZIP and metrics verified (2026-11-18); canary redaction unit tests | PASS |
| Graceful shutdown | Automated container shutdown + DB reopen test; live process exit still to verify | Partial |

**Important:** A passing smoke script is not the same as a passing real-model end-to-end test. Record skipped gates explicitly, including the reason.
