# P5 Product-Level End-to-End Acceptance

**Target branch:** `p5/control-plane`  
**Baseline:** Part 1 and Part 2 accepted; start from the current branch head.  
**Purpose:** Validate the running product across HTTP, Dashboard, provider execution, usage accounting, backup/restore and operations. Unit/race tests alone do not satisfy this gate.

## 1. Automated smoke test

Start AgentMesh using the same config and database you intend to validate. Keep it bound to loopback unless remote access has been separately hardened.

The API token must be the same token already configured in the running AgentMesh service. The backup passphrase is a separate password used only to encrypt this test backup. Run the script directly: it prompts for both values with terminal echo disabled. Neither value needs to be exported or added to a config file or shell history.

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
4. Run `AGENTMESH_API_TOKEN="$AGENTMESH_API_TOKEN" go run ./cmd/agentmesh-ops status`.
5. When ready to stop the service, use the Dashboard shutdown confirmation or `go run ./cmd/agentmesh-ops shutdown`; verify the process exits cleanly and its database can be reopened.

## 6. Acceptance record

Do not mark P5 product-level acceptance complete until all rows are backed by observed evidence.

| Gate | Evidence | Status |
|---|---|---|
| Automated runtime smoke | `accept-p5-e2e.sh` output | Pending |
| Dashboard navigation | Pages loaded with authenticated session | Pending |
| Real provider connectivity | Successful provider test | Pending |
| Real Agent/Workflow run | Execution ID + terminal status | Pending |
| Timeline and evidence | Execution detail/history inspected | Pending |
| Usage accounting | Before/after usage snapshot | Pending |
| Backup and restore | Verified archive + isolated restore | Pending |
| Diagnostics redaction | Report and ZIP inspected | Pending |
| Graceful shutdown | Process exit + DB reopen | Pending |

**Important:** A passing smoke script is not the same as a passing real-model end-to-end test. Record skipped gates explicitly, including the reason.
