# P4 — Unified Execution Core

> Status: architecture baseline. Implementation starts only after the M42 contract audit. This phase is additive to the accepted P3 baseline.

P4 turns the existing Execution/Attempt management layer into the durable semantic core shared by Agent, Coding, Workflow and CLI execution. It does not create another scheduler or generic job queue.

## 1. Baseline and non-goals

P3 is the accepted collaboration baseline. Preserve its current package boundaries, migrations, APIs and runtime-specific queues unless a concrete defect or compatibility requirement proves a change necessary.

P2 already provides:
- durable Execution and Attempt identities;
- runtime adapters for Agent, Coding, Workflow and CLI;
- idempotent submission;
- observation, cancellation and reconciliation;
- explicit operator resolution for uncertain outcomes.

P4 must extend these capabilities rather than duplicate them.

Non-goals:
- no second generic job queue;
- no replacement of Agent Scheduler, Coding Service, Workflow queue or CLI executor;
- no Redis, Kafka, GORM or mandatory user/account system;
- no automatic replay of an execution with uncertain external side effects;
- no broad directory or database redesign.

## 2. Canonical execution contract

The shared domain contract is:

- **Task**: durable intent/work request. Runtime-specific task records remain authoritative for runtime configuration and mechanics; the common layer references them.
- **Execution**: one accepted attempt to fulfill a Task under a selected runtime and policy.
- **Attempt**: one concrete runtime start/outcome within an Execution. Attempt history is append-only once terminal or unknown.
- **Execution Event**: an ordered, durable record of a lifecycle transition or significant execution observation. Events are append-only and carry actor/source, timestamp, correlation and structured payload.
- **Evidence**: a typed, attributable reference supporting a claim about an execution or outcome (logs, test results, tool output, checks, approvals, external references). Evidence is not itself a success verdict.
- **Artifact**: a versioned output produced by execution. Existing Artifact ownership/version semantics remain authoritative; P4 links artifacts to executions and attempts rather than cloning their storage model.

The core must preserve provenance:
Task -> Execution -> Attempt -> Event / Evidence -> Artifact Version -> Verification / Review.

A successful Execution must be explainable from its runtime outcome and recorded evidence. Missing or contradictory evidence must not be silently converted into success.

## 3. Lifecycle rules

- Execution state is a projection of accepted lifecycle facts, not an independently mutable label.
- Runtime adapters report facts; the Execution Manager validates transitions and persists them.
- Duplicate delivery is idempotent using stable event identity or runtime observation identity.
- Terminal Attempt records are immutable. Corrections are represented by new events or explicit operator resolutions, never by rewriting history.
- Unknown/interrupted outcomes remain distinct from failed outcomes.
- Retry creates a new Attempt under the same Execution only when policy and runtime semantics explicitly permit it; otherwise it creates a new Execution linked to the same Task.
- Cancellation is a request followed by an observed outcome, not an immediate assertion that work stopped.
- External side effects are at-least-once/uncertain unless the runtime can prove stronger idempotency.

## 4. Milestones

### M42 — Contract and implementation audit
Map current Task, Execution, Attempt, Event, Evidence, Artifact, Verification and Review models, stores, adapters, routes and tests. Produce a field/transition matrix and identify duplicated or missing semantics. No schema change by default.

### M43 — Durable execution event ledger
Add a single append-only event contract and persistence only where the audit proves existing event stores cannot satisfy the shared contract. Define ordering, idempotency, correlation, actor/source and retention. Keep runtime-specific event stores authoritative where possible and project them through adapters.

### M44 — Evidence and artifact provenance
Connect evidence and versioned artifacts to Execution and Attempt with stable provenance. Reuse existing collaboration Evidence and Artifact models. Enforce that evidence references are attributable and that artifact versions cannot be silently overwritten.

### M45 — Lifecycle and transition enforcement
Centralize legal Execution/Attempt transitions, cancellation observation, timeout semantics, retry eligibility and terminal immutability. Runtime-specific workers remain responsible for actual execution.

### M46 — Recovery and reconciliation
Unify startup/periodic reconciliation around adapter observations and durable events. Never blindly replay uncertain work. Add operator-visible reconciliation reasons and audited resolutions.

### M47 — Cross-runtime integration and observability
Provide consistent detail/history views across Agent, Coding, Workflow and CLI: Task, Execution, Attempts, Events, Evidence and Artifacts. Add correlation IDs and stable filtering without replacing runtime-native APIs.

### M48 — Release acceptance
Add cross-runtime end-to-end scenarios for success, failure, cancellation, timeout, retry, duplicate observation, process restart, uncertain outcome, evidence-backed artifact acceptance and manual resolution. Run sqlc generation, formatting, build, vet, race tests and acceptance scripts on Go 1.27.3; verify CI against the exact release commit.

## 5. Engineering constraints

- Go 1.27.3; modular monolith.
- Local-first; SQLite with modernc.org/sqlite; no CGO requirement.
- SQL migrations through golang-migrate; sqlc is the default typed query layer.
- Do not add GORM, Redis or Kafka.
- Preserve current API compatibility; additive endpoints/fields are preferred.
- Every migration requires up/down scripts, sqlc regeneration and migration tests.
- Every behavior change requires unit/integration coverage and an acceptance scenario.
- Keep credentials and secrets out of logs, events and evidence payloads.
- Keep the service loopback-bound by default; optional remote access remains API-token protected.

## 6. Definition of done

P4 is complete when all four runtimes expose the same explainable execution history and provenance contract; lifecycle transitions are validated and auditable; restart recovery does not invent outcomes or duplicate side effects; evidence and artifact versions can be traced to the Attempt that produced them; and the full acceptance suite passes on the exact release commit.

## 7. Part 1 implementation record (M42–M44)

The source-level audit is recorded in [M42 Execution Contract Audit](p4-m42-audit.md).

The P4 branch contains the following additive implementation:

- Migration `000039_execution_event_ledger` captures Execution/Attempt creation, status changes and runtime-reference binding in an append-only SQLite ledger. The ledger has a global monotonic sequence; query consumers filter by Execution and use sequence as the ordering/cursor key.
- `GET /api/v1/managed-executions/:id/events?limit=50&after_sequence=0` exposes bounded, ordered history. It is protected by the same optional API-token middleware as the rest of the API.
- Migration `000040_execution_artifact_provenance` adds Attempt lineage to Artifact Versions and Execution/Attempt lineage to Evidence, with a legacy Evidence backfill from source Execution.
- Migration `000041_execution_provenance_immutability` validates provenance updates and prevents rewriting a bound source Execution/Attempt.
- `GET /api/v1/collaboration/artifacts/:id/provenance` reads Artifact Version and Evidence lineage without duplicating Artifact content, Verification or Review storage.
- `POST /api/v1/collaboration/artifacts/:id/versions/:version/provenance` binds an existing Artifact Version to its producing Execution/Attempt. Repeating the same binding is idempotent; a conflicting binding is rejected.
- `POST /api/v1/collaboration/artifacts/:id/evidence/provenance` creates version-scoped Evidence with optional validated Execution/Attempt lineage. Legacy/manual Evidence without provenance remains supported.

M43/M44 implementation tests are included for event ordering/pagination, append-only ledger behavior, provenance binding, mismatch rejection and provenance reads.

**Verification status:** code and tests have been committed to the P4 branch. Run `bash scripts/verify-p4-part1.sh` from the repository root. No local Go command or CI result has yet been observed for these commits; do not treat this record as a passing build/race/acceptance result.
