# P4 Part 2 — Lifecycle Enforcement and Recovery

> Implementation record for M45–M46. Verification must be run on the target Go 1.27.3 environment using `bash scripts/verify-p4-part2.sh`.

## M45 — Lifecycle enforcement

Migration `000043_execution_lifecycle_enforcement` moves lifecycle invariants into SQLite so they apply to every writer, not only the Manager:

- Execution transitions are constrained at the persistence boundary. Queued may start or close; Running may close or become interrupted/needs-reconciliation; Interrupted and NeedsReconciliation may only be closed by an explicit resolution.
- Succeeded, Failed, Canceled and TimedOut executions are terminal and cannot be reopened.
- Running Attempts may be completed once as Succeeded, Failed, Canceled, TimedOut or Unknown. Terminal/Unknown Attempts cannot be reopened.
- Execution identity, accepted input and creation time are immutable. Attempt identity, input and start time are immutable.
- Domain `CanTransition` mirrors the persistence rules. Running-to-Queued is forbidden: retries are represented by another Attempt, not by erasing the fact that work already started. Agent scheduler retry-wait and pre-start lease states are projected without regressing the parent Execution; the completed Attempt is closed and the next Attempt is appended only after the worker crosses its execution boundary.

The database triggers are intentional defense in depth. Manager validation provides domain errors; SQLite protects against alternate writers and future code paths.

## M46 — Recovery and cancellation semantics

### Cancellation

- `Manager.Cancel` writes a durable `execution.cancel_requested` control event before invoking the runtime adapter.
- The event is idempotent per Execution and append-only.
- An adapter returning nil means the cancellation request was delivered/accepted by that adapter; it does not change the Execution status.
- The Execution becomes Canceled only after a subsequent runtime observation confirms that outcome. A failed cancellation call leaves the request event available for audit and retry.

Control events currently live in `execution_control_events`, separate from the M43 lifecycle ledger because the original M43 event-type CHECK constraint is intentionally not rewritten. M47's cross-runtime history view must merge these streams by their persisted sequence/time metadata.

### Ambiguous submission and recovery

- A Submit error is not automatically a Failed outcome: the runtime may have accepted work before the response was lost. The Manager records `needs_reconciliation` with the reason.
- A process crash after the Execution row is created but before Submit is completed leaves a Queued row. Reconciliation may call Submit again only under the Adapter contract that the same durable Task/Execution identity is idempotent.
- If queued submission cannot be confirmed, the Execution is moved to `needs_reconciliation`; it is not retried indefinitely.
- If an active Execution cannot be observed, the reason is persisted and the Execution moves to `needs_reconciliation`.
- Missing adapters and failed runtime-reference binding also become explicit reconciliation cases.
- Running executions are never resubmitted. Unknown/interrupted outcomes require operator resolution; Resolve records an explicit decision and leaves an uncertain Attempt as Unknown.

### Recovery state policy

| Situation | Action |
|---|---|
| Queued row after process crash | Idempotent Submit using stable runtime identity, then Observe |
| Submit response ambiguous | Mark NeedsReconciliation; do not infer Failed |
| Runtime observation unavailable | Mark NeedsReconciliation with reason |
| Runtime adapter unavailable | Mark NeedsReconciliation with reason |
| Running execution after restart | Observe only; never Submit again |
| Runtime confirms cancellation | Persist Canceled outcome through normal observation |
| Cancellation request fails | Keep request event; do not claim Canceled |
| Interrupted / unknown side effect | Require explicit operator resolution |

## Files and verification

- Migration: `internal/platform/database/migrations/000043_execution_lifecycle_enforcement.{up,down}.sql`
- Durable cancellation intent: `internal/execution/repository/control_events.go`
- Lifecycle/recovery rules: `internal/domain/execution/entity.go`, `internal/execution/manager/manager.go`
- Tests: Manager cancellation/ambiguous-submit/reconciliation tests and SQLite migration/trigger integration test.
- Acceptance: `scripts/verify-p4-part2.sh` runs gofmt, build, vet and the full race suite. The SQLite integration test runs all migrations and verifies cancellation idempotency, append-only control events, terminal immutability and immutable accepted input.

No test result is implied by this document; the acceptance script must pass on the exact synchronized commit.
