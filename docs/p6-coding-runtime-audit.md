# P6 — Coding Runtime Source Audit and Implementation Record

**Baseline:** `p5/control-plane`  
**Implementation branch:** `p6/coding-runtime`  
**Scope:** Coding Task lifecycle, Unified Execution adapter, Workflow Coding node, Worktree, Docker Sandbox, Artifact/Evidence provenance, Dashboard, migrations and acceptance gates.

## 1. Executive conclusion

P6 extends the existing Coding Runtime. It does not add another execution manager, queue, event ledger, workflow engine or artifact store.

The implementation now connects the existing components into one path:

```text
Dashboard / Workflow
        |
        v
Unified Execution Manager (Execution + Attempt)
        |
        v
CodingAdapter
        |
        v
Coding Service
  |       |         |
  |       |         +--> Docker Sandbox tests
  |       +------------> Agent Runtime / Model Gateway
  +--------------------> Git Worktree + durable Steps/Events/Snapshots
        |
        v
Collaboration Artifact Version + Evidence
        |
        v
Unified Execution Detail / Timeline
```

The P6 code is present on this branch. A green GitHub Actions run and the user's live Docker/model acceptance are still required before P6 can be called accepted.

## 2. M57 — Durable Coding Execution

### Implemented

- Coding Tasks persist their canonical `execution_id` and `attempt_id`; the adapter binds those values after the Execution Manager accepts the request.
- Adapter preflight validates the request and canonical repository path before creating the canonical Execution, avoiding orphan Executions for invalid repository paths.
- `CodingAdapter.Observe` creates the canonical Attempt before publishing P4 Artifact/Evidence provenance when a task completed before its first observation.
- Repeated Submit is idempotent. A previously bound pending task is re-scheduled by Observe after process restart.
- A task canceled before it starts is reported as `AttemptNotStarted`; it does not receive a fictitious Attempt.
- Explicit user cancellation remains `canceled`. Service shutdown records `interrupted` with `outcome_unknown=1`; it is not misreported as a user cancellation.
- Interrupted tasks retain their Worktree and are not automatically replayed. Startup also inspects the deterministic Worktree path and reattaches it to the task if the process crashed after `git worktree add` but before the path/branch metadata was persisted.
- The Dashboard exposes an explicit operator resolution after the diff and step history are visible: `keep` retains the Worktree and resolves the task/Execution as failed; `discard` resets the Worktree and resolves them as canceled. Neither option can mark an interrupted Coding Task successful.
- Coding Task events and snapshots are append-only/immutable at the SQLite layer.
- Coding model calls carry Execution/Attempt provenance into the Usage Ledger.
- Test and reviewer output are bounded before persistence; patch size is capped before artifact publication.

### Migrations

| Version | Purpose |
|---|---|
| 000045 | Coding Execution linkage, unknown outcome and append-only event/update protection |
| 000046 | Test exit code and duration |
| 000047 | Coding Attempt linkage |
| 000048 | Snapshot Execution/Attempt provenance and content digest |
| 000049 | Snapshot delete protection |
| 000050 | Coding Step immutability |
| 000051 | Model Usage Attempt index |

Migration versions are unique on this branch.

## 3. M58 — Durable Workflow Coding node

### Implemented

- Workflow definitions support a typed `coding` node with repository, model, prompt, test command, repair budget and timeout.
- The compiler validates the node contract.
- Workflow execution invokes Coding through the existing Execution Manager using a stable Workflow Run + Node task identity.
- The Workflow node output persists the Coding Task and canonical Execution references.
- When a worker lease expires, a running Coding node is rejoined through the same idempotency key. A completed Coding Execution is consumed; an interrupted/unknown outcome is surfaced and is not blindly replayed.
- Explicit Workflow cancellation cancels the child Coding Execution. Worker/process shutdown does not convert that child Execution into a confirmed user cancellation.
- Recovered Coding node output is restored into Workflow context so downstream nodes can continue.

The Workflow Runtime continues to own its existing durable run queue, lease, checkpoint and node ledger.

## 4. M59 — Test, repair, review and provenance gate

### Implemented

- Tests run through the configured Docker Runner, not through a host shell.
- Test exit code, duration, bounded output and each repair iteration are persisted as Coding Steps.
- Repair attempts remain bounded by the configured 0–5 limit.
- A separate read-only Reviewer invocation inspects the actual patch and test evidence. It returns a structured approval/rejection verdict.
- A managed Coding success requires test evidence and reviewer evidence. No detected test command is a failure for a reviewed managed task; the Dashboard/API can provide an explicit test program and arguments.
- A successful result publishes a deterministic Collaboration Artifact Version with `text/x-diff` content and canonical Execution/Attempt provenance.
- Diff, test and reviewer evidence are published through the existing Collaboration service and verified before the Execution is reported succeeded.
- Artifact and Evidence IDs are deterministic per Coding Task, making retries idempotent.
- Artifact Version insertion persists both `source_execution_id` and `source_attempt_id`.

No second artifact table or parallel provenance system was introduced.

## 5. M60 — Coding Dashboard and acceptance

### Dashboard

The existing HTMX Dashboard includes `/dashboard/coding`:

- Create and list Coding Tasks
- Repository path, model, prompt, optional test command, repair budget and timeout
- Task detail, canonical Execution link, Attempt, Worktree/branch, Steps, test output, reviewer output, events, snapshots and final diff
- Cancel, revert and cleanup controls using POST and confirmation
- Docker image and network mode disclosure
- User-visible operation feedback

All browser assets remain under repository-root `web/`.

### Live acceptance

`scripts/accept-p6-coding.sh` now exercises a durable Workflow containing a real Coding node. It verifies:

1. Workflow definition and run
2. Real Coding Task and canonical Execution/Attempt
3. Docker test success
4. Reviewer approval
5. Final Worktree diff and snapshot
6. P4 Artifact Version and diff/test/reviewer Evidence provenance

The script never commits or pushes. It retains the Worktree for inspection.

## 6. Repository authorization and sandbox boundary

Coding Task creation is deny-by-default in the application bootstrap:

```yaml
coding:
  allowed_repository_roots:
    - /absolute/path/to/trusted/repositories
```

An empty list disables Coding Task creation. Configured roots must exist. Repository paths and Git top-level paths are canonicalized; symlinks cannot escape an approved root. The same check runs during Execution preflight and again when the Coding Task is created.

Docker remains a constrained execution environment, not a perfect hostile-code boundary:

- Network is disabled by default; enabling it uses Docker bridge networking and is shown in the Dashboard (`agent.tools.sandbox_network_enabled: true`). Writable Go module and Rust Cargo caches are ephemeral tmpfs mounts; they do not persist between test containers. With networking disabled, projects must vendor/preload dependencies or use an offline test command.
- Container root filesystem is read-only.
- Capabilities are dropped and no-new-privileges is enabled.
- PID, memory and CPU limits are applied.
- Only the task Worktree is mounted writable.
- The host Docker daemon and configured image remain trusted components.

AgentMesh does not mount provider credentials, its database, encryption keys or the Docker socket into the task container. P6 does not automatically commit, push, create PRs or merge.

## 7. Verification state and remaining gates

- P5 baseline was accepted separately; P5 acceptance is not evidence of live Coding acceptance.
- P6 CI runs formatting, shell checks, the complete P5 verification suite and P6 verification.
- The opt-in live gate requires a running AgentMesh instance, a configured provider/model, Docker daemon access, an approved repository root and an explicit test command if the project manifest cannot be detected.
- No claim of live Docker/model success is made until the user runs `scripts/accept-p6-coding.sh` and the script passes.
- Docker daemon/image trust, single-host execution and local filesystem access remain explicit deployment boundaries.

## 8. Non-goals retained

- No new generic queue, Execution Manager, event ledger, Workflow engine or artifact store.
- No change to SQLite-first, sqlc, Go templates or HTMX.
- No automatic commit, push, PR or merge.
- No public multi-tenant control plane or multi-machine Runner in P6.
