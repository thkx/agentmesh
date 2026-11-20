# P6 — Coding Runtime Closed Loop (M57–M60)

**Base branch:** `p5/control-plane`  
**Implementation branch:** `p6/coding-runtime`  
**Goal:** make AgentMesh complete a real, reviewable coding task against a selected local Git repository, with durable execution semantics, sandboxed tests, bounded repair, provenance and a usable Dashboard.

The source audit is in [P6 Coding Runtime Source Audit](p6-coding-runtime-audit.md). This plan is intentionally additive: it closes gaps in the existing Coding Runtime rather than replacing it.

## 1. Product outcome

A user should be able to:

1. Select an explicitly authorized local Git repository and model.
2. Describe a coding task and submit it from the Dashboard or API.
3. Inspect the durable Coding Task / canonical Execution relationship.
4. Let AgentMesh create a task-specific Git Worktree and implement the change.
5. Run tests/build checks in the configured Docker Sandbox.
6. Allow a bounded repair loop when tests fail, without weakening or deleting tests.
7. Inspect the final diff, base commit, test output, repair history and review result.
8. Decide whether to keep the Worktree changes, revert them, or clean up the Worktree. AgentMesh does not auto-commit or push.

## 2. Architecture and ownership

```text
Dashboard / API
    |
    v
Execution Manager (canonical Execution / Attempt / lifecycle)
    |
    v
Coding Runtime Adapter
    |
    v
Existing Coding Service + coding_tasks store
    |                 |
    |                 +--> Git Worktree Manager
    |                 +--> Agent Runtime / Model Gateway
    |                 +--> Docker Sandbox Runner
    |                 +--> Coding Steps / Events / Snapshots
    |
    +--> Collaboration Artifact / Evidence (P4 provenance)
    +--> Model Usage Ledger (P5 provenance)

Workflow Runtime (existing durable queue and node ledger)
    |
    +--> typed Coding activity integration (M58)
```

Ownership rules:

- Execution Manager remains the sole owner of canonical Execution and Attempt state.
- Coding Service remains the owner of Coding Task mechanics, Worktree, test/repair steps and Coding Task events.
- Workflow Engine remains the owner of durable Workflow runs, leases, checkpoints and node observations.
- Collaboration remains the owner of Artifact Versions, Evidence, Verification and Review.
- Usage Ledger remains the owner of model usage/cost records.
- Dashboard is a presentation/application adapter; it calls services and does not mutate runtime tables directly.


Example Coding Workflow node:

```yaml
- id: implement
  type: coding
  coding:
    repository_path: /Users/your-user/src/example
    model_id: glm-5.3-flash
    prompt: "Implement the requested change and preserve existing behavior."
    test_command:
      program: go
      args: ["test", "./..."]
    max_repair_attempts: 2
    timeout_seconds: 1800
```

The repository path must be inside `coding.allowed_repository_roots`. Test arguments are passed as an argv array; shell metacharacters are rejected and no shell command string is evaluated.

## 3. Milestones

### M57 — Durable Coding Execution and provenance

**Purpose:** make Coding Task lifecycle facts truthful and recoverable under the P4 contract.

Deliverables:

- Add an explicit Coding Task `interrupted` state (additive SQLite migration; preserve existing task rows and indexes).
- Change restart recovery to atomically persist the interrupted state and a corresponding Coding Task event. Retain the Worktree for inspection.
- Map Coding `interrupted` to canonical Execution `interrupted`; the running Attempt becomes `unknown`, not `failed`.
- Make local concurrency saturation a recoverable queued condition. A Coding Task must not become permanently `needs_reconciliation` merely because both in-process slots are occupied. Reuse the existing persisted pending tasks; do not add a second queue table.
- Persist the canonical Execution ID on the Coding Task and propagate Execution/Attempt provenance through the Coding Agent context into Model Gateway Usage events.
- Add database-level append-only guards for Coding Task events and immutable snapshot semantics where compatible with existing cleanup/retention behavior.
- Keep repair iterations as Steps under the same runtime Attempt; ensure each iteration has an ordered, durable step/event record.
- Add adapter/service/repository tests for duplicate Submit, capacity pressure, restart interruption, unknown Attempt outcome, cancellation and provenance.

Acceptance:

- A process restart during a Coding Task yields canonical `interrupted` / Attempt `unknown`, never a false success or confirmed failure.
- Repeated Submit/reconciliation for the same Execution does not create a duplicate Coding Task or duplicate Worktree.
- Saturation leaves work recoverable and does not strand the Execution.
- Model Usage events from Coding runs carry the canonical Execution/Attempt IDs.
- Existing P2/P4 lifecycle invariants remain unchanged.

### M58 — Workflow-to-Coding orchestration

**Purpose:** let the existing durable Workflow Runtime orchestrate a real Coding activity.

Deliverables:

- Extend the existing Workflow node contract/compiler with a typed Coding activity; do not introduce a new Workflow engine or queue.
- Define a bounded Coding node input contract: authorized repository/workspace reference, model ID, prompt, optional explicit test command, timeout and repair budget.
- Execute through the existing Coding Service and record the Coding Task ID in the Workflow node output.
- Persist node-to-Coding-Task linkage in the existing Workflow node ledger/checkpoint output so the worker can observe an already-created Coding Task after a restart rather than blindly creating a second one.
- Reconcile node state from Coding Task state: completed tasks may be consumed; active or interrupted tasks with uncertain side effects must not be blindly replayed.
- Preserve Workflow approval/policy checks before repository mutation or network-enabled sandbox use.
- Add integration tests for success, test failure, cancellation, duplicate delivery and process-restart recovery.

Acceptance:

- A durable Workflow can run a Coding activity and return its task/execution references.
- Reprocessing the same Workflow node does not create duplicate Coding Tasks or Worktrees.
- An ambiguous Coding outcome blocks safe continuation and is surfaced for operator attention.

### M59 — Test, repair and review quality gate

**Purpose:** make the generated patch explainable and reviewable, not merely “the Agent returned text”.

Deliverables:

- Persist explicit test/build command, exit status, bounded stdout/stderr, duration and attempt number for every test iteration.
- Preserve the existing 0–5 repair ceiling; make retry count and timeout visible and enforce them server-side.
- Add a distinct Reviewer step after successful tests. The Reviewer inspects the actual diff and recorded test evidence, and returns a structured verdict with findings.
- A failed test or rejected review must not be reported as Coding success.
- Reviewer output is advisory evidence, not an automatic permission to commit, push or merge.
- Bind the final patch and test/review evidence to existing Collaboration Artifact Version / Evidence records with canonical Execution and Attempt provenance. Do not add a second artifact store.
- Enforce content digest and provenance consistency using the existing P4 mechanisms.

Acceptance:

- A successful Coding result has a persisted base commit, final diff digest, test evidence and review verdict.
- Failed tests remain visible with their original output; repairs cannot erase or rewrite previous evidence.
- Review rejection leaves the Worktree available and the result non-accepted.
- Artifact Version and Evidence reads resolve to the exact Execution/Attempt that produced them.

### M60 — Coding Dashboard and real-project acceptance

**Purpose:** deliver the complete user-facing coding loop and prove it against a real repository.

Deliverables:

- Add a Coding section to the existing Dashboard: task list, create form, detail, lifecycle, Steps, Events, repair iterations, test output, Worktree/branch, base commit and diff.
- Provide safe controls for cancel, patch inspection, revert and cleanup; destructive actions require confirmation and POST.
- Link each Coding Task to its canonical Execution detail/timeline and published Artifact/Evidence.
- Show sandbox network mode and configured image identity; never imply that network-enabled execution is isolated from external services.
- Add a deterministic fake-Agent/Git acceptance suite and a separate opt-in live acceptance script using a configured real model and Docker image.
- Live acceptance must create a disposable local Git repository, make a bounded code change, run tests in Docker, exercise a repair case, persist the final diff/evidence and verify all links. It must not commit or push.
- Update docs, example configuration and the P6 verification entry point.

Acceptance:

- User can create, run, inspect, cancel and clean up a Coding Task without curl.
- A real model + real Coding Runtime + Docker Sandbox completes a repository change with test and review evidence.
- Canonical Execution, Attempt, Timeline, Usage, Coding Task, Worktree diff and Artifact provenance agree.
- Full Go formatting, sqlc consistency, build, vet, race suite and P6 acceptance pass on the exact release commit.

## 4. Security and safety requirements

- Repository selection must be constrained to configured/approved roots or an explicit repository capability; an arbitrary path from an untrusted HTTP request is not sufficient authorization.
- Resolve and validate repository paths and symlinks before creating a Worktree. Prevent Worktree roots from overlapping protected AgentMesh data/config/key directories.
- Docker remains the execution boundary for tests. Default network mode remains disabled. Enabling network requires explicit configuration and visible UI disclosure.
- Keep the container root read-only, capabilities dropped, no-new-privileges, resource limits and only the task Worktree mounted writable. Consider non-root UID and digest-pinned images as hardening; document host Docker daemon trust.
- No provider credentials, API tokens, host home directory, Docker socket or AgentMesh database/key directories are mounted into the sandbox.
- Limit patch size, test output, diff size, task timeout, concurrent tasks and repair attempts.
- No automatic commit, push, PR creation or merge in P6.
- Preserve cancellation as a request followed by observed runtime outcome.

## 5. Schema policy

P6 is not a broad database redesign. Expected schema work is limited to additive migration(s) for:

- Coding Task interrupted-state support and canonical Execution linkage;
- any narrowly required node-to-Coding-task durable linkage not already expressible in the Workflow node ledger;
- immutable Coding event/snapshot guards.

Artifact and Evidence must reuse the existing Collaboration tables and P4 provenance fields. Do not create a new generic artifact table or duplicate Execution event stream. Before adding any Workflow linkage column, first prove the current node ledger output cannot safely carry the stable Coding Task ID.

## 6. Delivery and verification

The implementation branch is `p6/coding-runtime`. Work proceeds in order M57 → M58 → M59 → M60, but each milestone must be integrated and tested before the next depends on it.

Required verification includes:

- `gofmt -l .` (check only; no auto-format masking)
- `sqlc generate` and generated-code consistency check
- `go test -race ./...`
- `go build ./...`
- `go vet ./...`
- deterministic Git/Worktree and fake-Agent acceptance
- opt-in live Coding acceptance (real model + Docker)
- Dashboard HTTP/auth/CSRF/escaping tests
- restart and duplicate-delivery recovery tests

A code commit or successful CI run is not a substitute for the user's live Docker/model acceptance. Every report must name the exact branch and commit, commands actually executed, skipped prerequisites and resulting evidence.


## 8. Implementation status on `p6/coding-runtime`

The following implementation work is now present in the branch:

- **M57:** Coding Tasks persist canonical Execution and Attempt IDs; Coding model calls and reviewer calls receive Model Gateway execution provenance; restart recovery persists a logical `interrupted` / unknown outcome and an append-only event; the Coding Adapter maps that state to P4 Interrupted and ensures a terminal Attempt exists before finalization; tasks wait for local concurrency capacity rather than being rejected when both slots are busy.
- **M58:** `NodeCoding` is part of the Workflow domain/compiler/runtime. The app-owned bridge submits through the existing Execution Manager with a deterministic task ID and idempotency key, observes the canonical Execution, and refuses automatic replay for interrupted or ambiguous outcomes.
- **M59:** test steps retain command, exit code, duration and output; a separate read-only Reviewer Agent must return a structured `approve` verdict; final patch snapshots retain Execution/Attempt IDs and SHA-256; the existing Collaboration Artifact Version and Evidence services publish a code-patch artifact plus diff/test/reviewer evidence with the same canonical provenance and run deterministic artifact verification.
- **M60:** Dashboard Coding list/create/detail, Worktree/diff, Steps, Events, test/review evidence, cancellation, guarded revert and cleanup are available under `/dashboard/coding`. The page discloses Docker image and network mode, supports explicit allowlisted test commands, and links to the canonical Execution detail. Browser assets remain under `web/`.
- Added migrations `000045`–`000048`, adapter/workflow/artifact provenance tests, `scripts/verify-p6.sh`, and the opt-in real model + Docker gate `scripts/accept-p6-coding.sh`.

### Required local configuration

Coding repository access is deny-by-default. Add only trusted repository parent directories to `coding.allowed_repository_roots` in the local configuration, for example:

```yaml
coding:
  allowed_repository_roots:
    - /Users/your-user/src
```

Use the actual absolute path on the machine running AgentMesh. The service canonicalizes both roots and requested repositories and rejects symlink escapes. Keep Docker network disabled unless the task explicitly needs it.

### Verification status

The implementation and verification scripts have been committed to this branch, but **no Go test, race test, build, vet, real model run, or Docker live acceptance is claimed as executed by the assistant**. Run `bash scripts/verify-p6.sh` on the target machine. Then configure the variables documented in `scripts/accept-p6-coding.sh` and run with `AGENTMESH_RUN_P6_LIVE=1` for the real Coding gate. A PR is required to obtain the repository's pull-request CI result; the branch is not yet merged into `p5/control-plane`.

## 9. Explicit non-goals

- No new generic job queue, scheduler or Execution Manager.
- No rewrite of Agent Runtime, Workflow Runtime, Model Gateway or P5 Dashboard stack.
- No cloud control plane, multi-user tenancy or public-internet deployment work.
- No automatic commit/push/PR/merge.
- No replacement of SQLite, sqlc, Go templates or HTMX.
