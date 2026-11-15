# M42 — Execution Contract Audit

> Audit target: `p4/unified-execution-core`  
> Scope: current Go domain/services, persistence, migrations, HTTP routes and tests inspected for the M42–M44 handoff.  
> Status: audit findings recorded; this is not an implementation or test-pass claim.

## 1. Executive result

The repository already has the correct high-level layering:

- P2 owns the common Execution Manager and Execution/Attempt identities.
- Agent, Coding, Workflow and CLI retain their runtime-specific execution mechanisms.
- P3 owns collaboration Tasks, Assignments, Workspace governance, Artifact versions, Evidence, Verification and Review.
- Collaboration Outbox/Inbox are delivery mechanisms, not a substitute for a cross-runtime execution history.

The P4 branch already contains two additive schema migrations:

- `000039_execution_event_ledger`: SQLite triggers create append-only lifecycle events for common Execution/Attempt inserts and selected updates.
- `000040_execution_artifact_provenance`: adds Attempt provenance to Artifact Versions and Execution/Attempt provenance to Artifact Evidence.

These migrations are useful foundations, but the current Go contract is not yet fully wired to them. The remaining M43/M44 work is primarily repository/service/API integration, provenance validation and tests—not another schema redesign.

## 2. Canonical model mapping

| P4 concept | Existing implementation | Audit conclusion |
|---|---|---|
| Collaboration Task (intent) | `internal/collaboration`, `collaboration_tasks`, `Task` | Durable cross-agent objective with Workspace, parent lineage, status, priority and capability requirements. This is distinct from runtime task input. |
| Execution | `internal/domain/execution.Entity`, `executions` | Common accepted unit; records kind/runtime/runtime_ref/task_id/status/input/result/error/idempotency and timestamps. |
| Attempt | `internal/domain/execution.Attempt`, `execution_attempts` | Runtime-specific start/outcome under an Execution; unique by execution and attempt number. |
| Runtime adapter | `internal/execution/manager.Adapter` | Submit/Observe/Cancel abstraction. Observation is currently polled by Manager; there is no adapter event callback contract. |
| Execution Manager | `internal/execution/manager.Manager` | Owns submit, idempotency, observe, cancel, reconcile and operator resolution. Must remain the single common coordinator. |
| Collaboration Event | `collaboration_outbox` / `collaboration_inbox` | Durable notification delivery with retries/leases/dead letters. Mutable delivery metadata means this is not the immutable execution ledger. |
| Workflow/runtime events | Runtime-specific stores | Continue to serve runtime mechanics. P4 should project relevant facts into the common ledger rather than replace them. |
| Artifact | `collaboration_artifacts` | P3-owned artifact identity and review lifecycle. Reuse it. |
| Artifact Version | `collaboration_artifact_versions` | Immutable content/version and digest; already has `source_execution_id`. Migration 000040 adds `source_attempt_id`. |
| Evidence | `collaboration_artifact_evidence` | Bound to an exact Artifact Version; migration 000040 adds optional Execution and Attempt references. |
| Verification / Review | P3 Artifact verification and review records | Remain version-scoped decisions. They are not Execution success verdicts. |

Important distinction: `execution.Entity.TaskID` is a runtime task reference/string; it is not a foreign key to `collaboration_tasks`. P4 must not silently conflate these two Task concepts. A future explicit collaboration-task reference should be additive and named distinctly if required.

## 3. Persistence and migration findings

### P2 baseline

Migration `000023_unified_executions` creates:

- `executions`, including the four runtime kinds and explicit uncertain states;
- `execution_attempts`, unique on `(execution_id, attempt_number)`.

The SQLite repository uses conditional state updates and unique constraints for idempotency. Attempt creation handles at-least-once observer callbacks by returning the existing attempt for the same execution/number rather than reopening it.

### M43 foundation already present

Migration `000039_execution_event_ledger` creates a global monotonic sequence and per-execution query index. Triggers atomically append events for:

- Execution creation;
- Execution status changes;
- Runtime reference binding;
- Attempt creation;
- Attempt status changes.

Update/delete triggers protect the ledger from mutation. This is a sound initial event-capture mechanism because the event is committed in the same SQLite transaction as the source write.

Gaps to close in M43:

1. The current ledger only captures the five trigger-defined fact types; it has no explicit operator-resolution/cancel-request or adapter-observation event.
2. Trigger-generated rows use default actor/source values, so caller-supplied actor, source and correlation metadata are not yet propagated.
3. There is no common Go event type/repository/query service or HTTP history endpoint.
4. The current event payload is intentionally small; it does not yet carry a stable correlation ID or structured observation metadata.
5. Event sequence is global. Consumers must order/filter by the persisted sequence and execution ID; timestamps alone are not a safe ordering key.

Do not replace these triggers with a second event queue. Extend the existing ledger and keep its append-only semantics.

### M44 foundation already present

Migration `000040_execution_artifact_provenance` adds:

- `collaboration_artifact_versions.source_attempt_id`;
- `collaboration_artifact_evidence.execution_id`;
- `collaboration_artifact_evidence.attempt_id`;
- indexes and insert-time consistency triggers;
- a backfill from Artifact Version `source_execution_id` to Evidence `execution_id`.

Gaps to close in M44:

1. Current Artifact Go request/model code exposes `source_execution_id` but not `source_attempt_id`.
2. Current Evidence request/model code exposes only Artifact Version and source reference/digest/statement; it does not expose the new Execution/Attempt fields.
3. Existing insert triggers validate Attempt-to-Execution consistency, but equivalent update guards are absent. Provenance fields must not be mutable after insertion.
4. Service-level validation must verify that an Attempt belongs to the supplied Execution and that the Artifact Version's source Execution agrees. Database constraints remain the final integrity boundary.
5. Verification and Review remain pinned to Artifact Version; M44 should expose their lineage through that version rather than duplicate provenance columns.

## 4. Service, route and test mapping

| Area | Existing entry point | Required P4 extension |
|---|---|---|
| Execution persistence | `internal/execution/repository/sqlite.go` | Read/query event ledger; keep writes atomic through existing triggers or a transaction-aware event writer. |
| Execution coordination | `internal/execution/manager/manager.go` | Preserve current coordinator; attach actor/source/correlation context to accepted facts and distinguish request from observed outcome. |
| Execution HTTP | `internal/transport/http/handlers/managed_execution.go` | Add bounded event-history query without changing existing submit/list/detail/cancel/resolve contracts. |
| Route registration | `internal/transport/http/router.go` | Register the history endpoint through the existing ManagedExecution handler. |
| Artifact service | `internal/collaboration/artifacts.go` | Accept/return Attempt provenance and validate exact Execution/Attempt/Version associations. |
| Collaboration HTTP | `internal/transport/http/handlers/collaboration.go` | Preserve existing artifact endpoints; add only fields/queries needed for provenance. |
| Existing tests | `internal/execution/manager/manager_test.go`, `internal/execution/repository/sqlite_test.go`, collaboration integration tests | Add migration-backed ledger ordering/immutability/idempotency and provenance mismatch/immutability coverage. |

## 5. Adapter and lifecycle observations

The Manager's Adapter contract is currently `Submit`, `Observe`, `Cancel`. Manager polls `Observe`, creates an Attempt when a runtime reports running or a terminal outcome, then conditionally finishes the Attempt and transitions the Execution.

Existing safety behavior to preserve:

- queued observation does not synthesize a running Attempt;
- duplicate Attempt creation is idempotent by execution/attempt number;
- uncertain Execution states remain distinct from failed;
- operator resolution does not rewrite an unknown Attempt as runtime-reported success;
- reconciliation resubmits queued work only and observes running work instead of blindly replaying it.

The event ledger must reflect these facts. In particular, a cancellation request is not a cancellation outcome, and operator resolution must be an explicit auditable event.

## 6. Part 1 implementation decisions

1. Keep P2 Manager and the four runtime adapters as the only common execution coordination path.
2. Keep P3 Collaboration Task and Artifact ownership; do not create parallel Task, Artifact or Evidence tables.
3. Finish the existing M43 ledger foundation with typed Go models, bounded ordered reads, correlation/actor/source metadata and API exposure.
4. Finish the existing M44 provenance foundation with typed Attempt references, validation, immutable provenance guards and migration-backed tests.
5. Preserve old HTTP payloads and permit legacy/manual Evidence without Execution provenance.
6. Do not claim a successful Execution merely because an Artifact or Evidence row exists; verification and review remain separate version-scoped decisions.

## 7. Verification status

This audit was performed against source and migration files on `p4/unified-execution-core`. No Go command, local migration run, race test or CI run was executed as part of this audit. M42 is an implementation audit artifact, not a verification result.
