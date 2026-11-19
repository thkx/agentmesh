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

## 2. Manual provider and model inspection

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


## 4. Automated live Provider → Agent → Workflow acceptance

The live gate is implemented by `scripts/accept-p5-live.sh`. It uses the real configured AgentMesh process, its real SQLite database, the real Model Gateway and the running Workflow worker. It does not use test adapters.

Requirements:
- Start AgentMesh with the intended config and API token file.
- Keep the service on loopback, or use HTTPS for a separately secured remote deployment.
- Have `curl`, `jq` and `openssl` installed.
- Use an OpenAI-compatible API key and an upstream model identifier available to that key. The default endpoint is `https://api.openai.com/v1`; compatible HTTPS endpoints are also supported.
- The model/provider should permit short completion requests. This test makes several small paid (or provider-billed) calls.

Run:

```bash
git pull origin p5/control-plane
bash scripts/accept-p5-live.sh
```

The script prompts with terminal echo disabled for the AgentMesh API token and provider API key. It asks for the upstream model identifier and provider base URL. The API key is sent to AgentMesh's write-only credential endpoint and stored by the existing encrypted credential store; it is not put in shell history, process arguments, the Git repository or the evidence report. The script sends credentials only to HTTPS endpoints, except loopback HTTP.

The script creates uniquely named resources prefixed with `p5-live-`:
- a disabled provider, then its encrypted credential, then enables it after credential installation;
- a model mapped to the selected upstream model identifier;
- a workspace and a real Agent definition;
- a two-Agent-node Workflow with a durable queued Run.

It then verifies:
1. provider connectivity performs a real upstream completion;
2. Model Gateway returns a known marker from the upstream model;
3. a real Agent run succeeds and returns its marker;
4. a queued Workflow executes two real Agent nodes in dependency order and reaches `succeeded`;
5. workflow history includes events and node observations;
6. at least four real usage events exist for the created provider/model, with provider/model provenance.

A successful run writes a mode-0600 JSON evidence report under `data/p5-live-acceptance-*.json`. The report contains IDs and counts only, never credentials or prompts. The script deliberately leaves acceptance resources in place so they can be inspected in the Dashboard and correlated with the report. After review, manually delete the temporary Workflow, Agent definition, workspace, model, provider and stored provider credential. Do not delete them before retaining the evidence you need.

The script is an actual live gate, not a simulation. It can incur provider charges. It does not test every provider adapter, streaming, tool execution, external side effects, approval recovery, destructive restore apply or remote-access hardening.

Pass criteria:
- Script exits zero and prints `P5 Live Acceptance passed.`.
- Evidence report records a succeeded Workflow Run, at least two node observations and at least four model usage events.
- Dashboard inspection confirms the provider/model, Agent run, Workflow Run and usage records are visible and consistent.
- Any provider-specific unknown token usage or unknown pricing remains explicitly unknown; do not interpret unknown as zero cost.

## 5. Backup and restore drill

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

## 6. Diagnostics and graceful shutdown

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

## 7. Automated test coverage

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

## 8. Acceptance record


Do not mark P5 product-level acceptance complete until all rows are backed by observed evidence.

| Gate | Evidence | Status |
|---|---|---|
| Automated runtime smoke | User-run `accept-p5-e2e.sh` output (2026-11-18) | PASS |
| Dashboard navigation | User-run authenticated page checks (2026-11-18) | PASS |
| Real provider connectivity | User-run live acceptance, provider connectivity PASS (2026-11-19) | PASS |
| Real Model Gateway generation | User-run live acceptance, upstream completion PASS (2026-11-19) | PASS |
| Real Agent/Workflow run | Agent task `1e8f4f95cd64468da56da0fc42313326`; Workflow run `p5-live-run-1795097643-ed6879` succeeded | PASS |
| Timeline and evidence | 11 workflow events and 3 node observations recorded; Dashboard detail/Timeline inspection remains | Partial |
| Usage accounting | 5 live usage events with provider/model provenance | PASS |
| Backup and restore | User-run encrypted archive verification + isolated restore staging (2026-11-18) | PASS |
| Diagnostics redaction | Report, support ZIP and metrics verified (2026-11-18); canary redaction unit tests | PASS |
| Graceful shutdown | Automated container shutdown + DB reopen test; live process exit still to verify | Partial |

**Important:** A passing smoke script is not the same as a passing real-model end-to-end test. Record skipped gates explicitly, including the reason.

### P5 Live Acceptance evidence — 2026-11-19

User-run `scripts/accept-p5-live.sh` completed successfully against the local service at `http://127.0.0.1:8080`.

Evidence report: `data/p5-live-acceptance-1795097643-ed6879.json`

| Field | Recorded value |
|---|---|
| Created at | `2026-11-19T14:15:14Z` |
| Provider | `p5-live-1795097643-ed6879` |
| Model | `p5-live-model-1795097643-ed6879` (`glm-5.3-flash`) |
| Agent definition | `p5-live-agent-1795097643-ed6879` |
| Agent task | `1e8f4f95cd64468da56da0fc42313326` |
| Workflow | `p5-live-workflow-1795097643-ed6879` |
| Workflow run | `p5-live-run-1795097643-ed6879` |
| Workflow status | `succeeded` |
| Workflow events | 11 |
| Node observations | 3 |
| Usage events | 5 |
| Secrets recorded in report | `false` |

The live script passed provider connectivity, Model Gateway generation, real Agent execution, a two-Agent-node durable Workflow run, workflow history/observations, and Usage Ledger provenance. The JSON report records identifiers and counts only; it does not independently prove the contents of the Dashboard views.

