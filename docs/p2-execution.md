# P2 — Unified Execution Management

> Status: implemented on `p2/implementation`. The documentation baseline commit `599935ec41136f651dde45244e1002e70eb21ea4` completed the repository's Go CI workflow successfully (Build, Vet and Race tests). This is a branch-level verification result, not a claim that the branch has been merged to `main`.

P2 adds a common execution identity and lifecycle above the existing runtime queues. It does **not** replace the Agent Scheduler, Coding Task service, Workflow queue or CLI executor with a second generic job queue.

## 1. Architecture

- **Execution** is the durable, cross-runtime unit of work.
- **Attempt** is one concrete runtime attempt belonging to an Execution.
- **Runtime Adapter** translates the common contract to an existing runtime.
- **Execution Manager** coordinates submission, lookup, cancellation, observation, idempotency and reconciliation.
- Existing runtime-specific stores and workers remain authoritative for their own execution mechanics.

| Runtime | Existing execution mechanism |
|---|---|
| Agent | `agent_jobs` and Agent Runtime |
| Coding | `coding_tasks` and Coding Service |
| Workflow | `workflow_jobs` and Workflow Engine |
| CLI | Existing CLI executor |

Migration `000023_unified_executions` adds the common `executions` and `execution_attempts` persistence. Runtime references and idempotency constraints prevent conflicting associations and duplicate client submissions.

## 2. HTTP API

Routes are registered under `/api/v1/managed-executions`. When API token authentication is enabled, send `Authorization: Bearer <token>`.

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/v1/managed-executions` | Submit an execution |
| GET | `/api/v1/managed-executions` | List/filter executions |
| GET | `/api/v1/managed-executions/{id}` | Read execution and observe runtime |
| DELETE | `/api/v1/managed-executions/{id}` | Request runtime cancellation |
| POST | `/api/v1/managed-executions/{id}/resolve` | Record an explicit operator resolution |

### Submit

```http
POST /api/v1/managed-executions
Content-Type: application/json
Idempotency-Key: request-2026-001
```

```json
{
  "kind": "agent",
  "runtime": "agent",
  "task_id": "task-2026-001",
  "input": {
    "id": "task-2026-001",
    "model_id": "model-id",
    "workspace_id": "workspace-id",
    "prompt": "Inspect the project and summarize its architecture"
  }
}
```

Supported `kind` and `runtime` values are `agent`, `coding`, `workflow` and `cli`; they must match. `input` is the runtime-specific JSON payload. The `Idempotency-Key` header takes precedence over the optional `idempotency_key` body field.

A successful submission returns `202 Accepted` with the persisted Execution. Repeating the same idempotency key and payload returns the existing Execution. Reusing the key for a different payload returns `409 Conflict`.

### List and read

- `GET /api/v1/managed-executions?status=running&kind=agent&runtime=agent&limit=50&offset=0`
- `GET /api/v1/managed-executions/{id}`

List filters are optional. A detail read observes the underlying runtime and includes persisted Attempts.

### Cancel

`DELETE /api/v1/managed-executions/{id}` dispatches cancellation to the relevant runtime. A successful request returns `202 Accepted`; cancellation is asynchronous, so read the Execution again to observe its final state. Terminal executions cannot be canceled.

### Resolve an uncertain outcome

An execution in `interrupted` or `needs_reconciliation` is not automatically replayed. After inspecting runtime and artifact evidence, an operator can record an explicit outcome:

```http
POST /api/v1/managed-executions/{id}/resolve
Content-Type: application/json
```

```json
{
  "status": "succeeded",
  "result": {"verified": true, "artifact": "report.md"},
  "reason": "Verified the persisted artifact and its checks"
}
```

Only `succeeded`, `failed` or `canceled` are accepted. A successful resolution requires a valid JSON result; every resolution requires a non-empty reason. The historical uncertain Attempt remains `unknown`: operator resolution records the chosen Execution outcome and does not rewrite history to claim that the runtime reported success.

## 3. Lifecycle

Execution states:

- `queued`
- `running`
- `succeeded`
- `failed`
- `canceled`
- `timed_out`
- `interrupted`
- `needs_reconciliation`

Attempt states:

- `running`
- `succeeded`
- `failed`
- `canceled`
- `timed_out`
- `unknown`

`interrupted` and `needs_reconciliation` are intentionally distinct from `failed`: the system cannot safely assert what happened. A queued observation does not create a running Attempt. An Attempt is created when the runtime reports that work has started or reached an outcome. Duplicate observation for the same execution and attempt number is idempotent and does not reopen a terminal Attempt.

## 4. Recovery and safety

After adapters are registered, the manager reconciles persisted queued and running executions at startup and periodically while the application is running.

- Queued executions are resubmitted through their adapter; each adapter must be idempotent against the underlying runtime identity.
- Running executions are observed, not blindly replayed.
- Missing or ambiguous outcomes are surfaced for reconciliation rather than silently marked successful.
- Agent, Coding, Workflow and CLI retain their own queues, leases and runtime-specific recovery rules.
- CLI work whose process state is lost is marked interrupted rather than automatically restarted.
- Orderly shutdown cancels and waits for managed CLI work; Coding Service has its own bounded shutdown lifecycle.
- Queued Coding cancellation uses a conditional SQLite state transition and records its event transactionally.
- Uncertain external side effects are not promised to be exactly-once.

The common Execution record is a coordination and observability layer, not a replacement queue.

## 5. Verification

Run from the repository root using the Go version declared in `go.mod`:

```bash
bash scripts/verify-p2.sh
```

The script runs formatting, `go build ./...`, `go vet ./...` and `go test -race ./...`. For CI results, verify the exact commit SHA in the [Go CI workflow runs for the P2 branch](https://github.com/thkx/agentmesh/actions/workflows/go.yml?query=branch%3Ap2%2Fimplementation).

## 6. Scope boundaries

P2 standardizes execution identity, lifecycle, attempts, results and recovery visibility. It does not unify the internal queue implementations, guarantee exactly-once external side effects, automatically replay uncertain work, or merge the P2 branch into `main`.
