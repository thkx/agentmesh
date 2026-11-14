# P2: Unified Execution Management

P2 adds a common execution identity and lifecycle above the existing runtime queues. It does **not** replace the Agent Scheduler, Coding Task service, Workflow queue, or CLI executor with a second generic job queue.

## Architecture

- **Execution** is the durable, user-facing unit of work.
- **Attempt** is one concrete runtime attempt belonging to an Execution.
- **Runtime Adapter** translates the common contract to an existing runtime.
- **Execution Manager** owns submission, lookup, cancellation dispatch, lifecycle observation, idempotency, and startup reconciliation.
- Existing runtime-specific stores remain authoritative for their own queueing and recovery behavior.

Supported runtime mappings:

| Runtime | Existing execution mechanism |
| --- | --- |
| Agent | agent_jobs and Agent Runtime |
| Coding | coding_tasks and Coding Service |
| Workflow | workflow_jobs and Workflow Engine |
| CLI | Existing CLI executor |

The common persistence layer is introduced by migration 000023_unified_executions:

- executions stores the durable cross-runtime identity and lifecycle.
- execution_attempts stores individual attempts and their outcomes.
- Unique idempotency keys prevent duplicate client submissions.
- Runtime references and (runtime, task_id) are unique when present.

## HTTP API

All routes are under /api/v1/managed-executions. If API token authentication is enabled, send Authorization: Bearer <token>.

### Submit

POST /api/v1/managed-executions

Request:

```json
{
  "kind": "agent",
  "runtime": "agent",
  "task_id": "task-2026-001",
  "idempotency_key": "client-request-2026-001",
  "input": {
    "id": "task-2026-001",
    "model_id": "model-id",
    "workspace_id": "workspace-id",
    "prompt": "Inspect the project and summarize its architecture"
  }
}
```

kind and runtime must match (agent, coding, workflow, or cli). input is the runtime-specific JSON payload. The optional Idempotency-Key request header takes precedence over the body field.

A successful submission returns HTTP 202 Accepted with the persisted Execution. Repeating a request with the same idempotency key and same payload returns the existing Execution; reusing the key for a different request returns HTTP 409 Conflict.

### Read

- GET /api/v1/managed-executions/{id} — read the Execution, observe the underlying runtime, and include persisted Attempts.
- GET /api/v1/managed-executions?status=running&kind=agent&runtime=agent&limit=50&offset=0 — list executions. Filters are optional.

### Cancel

DELETE /api/v1/managed-executions/{id} dispatches cancellation to the underlying runtime and returns HTTP 202 Accepted when the cancellation request is accepted. Cancellation is asynchronous: read the Execution again to observe the final state. A terminal Execution cannot be canceled.

## Lifecycle

Execution lifecycle states:

- queued
- running
- succeeded
- failed
- canceled
- timed_out
- interrupted
- needs_reconciliation

Attempt states:

- running
- succeeded
- failed
- canceled
- timed_out
- unknown

interrupted and needs_reconciliation are deliberately distinct from failed: they indicate that the system cannot safely assert the outcome. Uncertain work is not automatically replayed.

A queued runtime observation does not create a running Attempt. An Attempt is created only after the runtime reports that work has started or reached an outcome. Duplicate observer delivery for the same (execution_id, attempt_number) is idempotent and does not reopen a terminal Attempt.

## Recovery and safety

At startup, and periodically while the application is running, the Execution Manager reconciles persisted queued and running executions after runtime adapters are registered. Reconciliation scans bounded pages and repeats after transient adapter failures:

- Queued executions are re-submitted through their runtime adapter. Adapters must make this operation idempotent against their existing runtime identity.
- Running executions are observed, not blindly replayed.
- Missing or ambiguous runtime outcomes are surfaced for reconciliation rather than silently marked successful.
- Workflow cancellation is delegated to the Workflow Engine, which handles queued and active runs.
- CLI work whose process state is lost is marked interrupted; it is not restarted automatically.
- CLI executions are canceled and awaited during orderly application shutdown; queued CLI executions can be canceled before a process is launched.
- Queued Coding tasks are canceled with a conditional SQLite state transition and an event in the same transaction, racing safely with worker claims.

Runtime-specific queues and recovery mechanisms remain authoritative. The common Execution record is an observable coordination layer, not a replacement queue.

## Verification

Run the P2 verification script from the repository root:

```bash
bash scripts/verify-p2.sh
```

It checks Go formatting, build, vet, and the full race-enabled test suite using the Go version declared in go.mod.


## Manual resolution

An execution in `interrupted` or `needs_reconciliation` is never automatically replayed. After inspecting runtime evidence, an operator can record an explicit outcome:

`POST /api/v1/managed-executions/{id}/resolve`

```json
{
  "status": "succeeded",
  "result": {"verified": true, "artifact": "report.md"},
  "reason": "Verified the persisted artifact and its checks"
}
```

Only `succeeded`, `failed`, or `canceled` are valid resolutions. A successful resolution requires a JSON result, and every resolution requires a reason. The original uncertain Attempt remains marked as unknown; the operator decision resolves the Execution, not the historical claim about what the runtime actually completed.
