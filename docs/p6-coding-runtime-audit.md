# P6 — Coding Runtime Source Audit

**Audit baseline:** `p5/control-plane`  
**Audit scope:** M35–M41 Coding Runtime, P2 Coding Adapter, P4 Execution/Attempt and provenance, Workflow Runtime, Dashboard, Worktree, Docker Runner, bootstrap and shutdown wiring.  
**Status:** Source inspection completed. This document is an audit record, not a claim that a new implementation or live Coding acceptance has passed.

## 1. Executive conclusion

Coding Runtime is a real, implemented subsystem, not a placeholder. It already creates isolated Git Worktrees, invokes the Agent Runtime, runs tests in Docker, performs bounded test-failure repair, stores task steps/events/snapshots, and exposes HTTP/CLI operations.

It is also registered with the P2/P4 Unified Execution Manager through `internal/execution/manager.CodingAdapter`. P6 must therefore close semantic and product gaps around this integration rather than introduce another Execution Manager, queue, lifecycle, or event ledger.

The most important correctness gap is restart semantics: interrupted Coding Tasks are currently rewritten to `failed`, then observed by the Coding Adapter as an ordinary failed outcome. This loses P4's distinction between a confirmed failure and an unknown/interrupted result. The second major gap is provenance: the final diff is stored in the Coding-specific snapshot table but is not automatically published as a P4 Artifact Version/Evidence record tied to the canonical Execution and Attempt.

## 2. What exists in the current source

| Area | Current implementation | Audit result |
|---|---|---|
| Coding Task persistence | `internal/coding/repository.go`, migration `000021_coding_tasks` | Real SQLite task, step, event and snapshot persistence |
| Task lifecycle | `internal/coding/service.go` | Pending/running/testing/repairing/succeeded/failed/canceled; bounded concurrency of two |
| Worktree | `internal/workspace/worktree/worktree.go` | Real `git worktree add -b`, diff, patch check/apply, reset and removal |
| Coding Agent | `internal/coding/service.go` | Calls the existing `Agent.Run` interface backed by Agent Runtime / Model Gateway; scopes its Agent task to the Coding Worktree |
| Docker tests | `internal/workspace/sandbox/docker/docker.go` | Real Docker CLI invocation; no host-shell interpolation; argv execution |
| Repair | `internal/coding/service.go` | Repeats test → Agent repair → test, bounded by `max_repair_attempts` (0–5) |
| Unified Execution | `internal/execution/manager/coding.go` | Real `CodingAdapter`; registered as `RuntimeCoding` during bootstrap |
| Shutdown | `internal/app/container.go` | Calls `CodingService.CloseContext` before database close |
| HTTP API | `internal/transport/http/handlers/coding.go` | Create/list/get/start/cancel/steps/events/snapshots/diff/patch/revert/cleanup |
| Dashboard | `internal/transport/http/dashboard/dashboard.go` | Unified Execution views exist, but no Coding Task management or Coding-specific detail view |
| Workflow | `internal/workflow/runtime/engine.go` | Durable workflow worker and node ledger exist; current engine executes Agent, Condition, Approval, Subworkflow and structural nodes, but has no Coding node |

## 3. Unified Execution integration: present, with gaps

The Coding Adapter is not merely a declared interface:

- `Bootstrap` constructs `CodingService`, creates `NewCodingAdapter(codingService)`, and registers it with `ExecutionManager.Register(RuntimeCoding, ...)`.
- `Submit` decodes the accepted Execution input, idempotently finds or creates the Coding Task, starts a pending task, and returns the Coding Task ID as the runtime reference.
- `Observe` maps Coding Task states to canonical Execution states and reports Attempt number 1.
- `Cancel` delegates to the Coding Service.

The current fixed Attempt number of 1 is consistent with the present Coding Service model: its repair loops are internal steps of one Coding Task execution, not separate P4 Attempts. P6 should preserve this distinction. A new P4 Attempt should only be created when a new runtime execution attempt is genuinely started, not for every internal test/repair iteration.

### Gaps

1. `RecoverInterrupted` updates active Coding Tasks to `failed` with the text `interrupted by AgentMesh restart`; it does not append a corresponding Coding Task event in the same transaction.
2. The Adapter maps every `StatusFailed` task to canonical `LifecycleFailed`. It cannot preserve an uncertain outcome as `LifecycleInterrupted` / `AttemptUnknown`.
3. The Adapter starts the Coding Task synchronously through `Start`. If the two local concurrency slots are full, `Start` returns `ErrConflict`; the Manager treats any Submit error as uncertain and transitions the canonical Execution to `needs_reconciliation`. The Manager's reconciler scans queued/running executions, not this state. Known local capacity pressure can therefore strand a task/execution instead of waiting in a recoverable queued state.
4. Coding Task / Agent Runtime calls do not currently receive the canonical Execution and Attempt provenance context used by the Model Gateway Usage Ledger. Coding model usage can therefore be missing the canonical Execution/Attempt association.
5. The Coding Task has no explicit persisted canonical Execution ID field. The common Execution references its Coding Task through `task_id` / `runtime_ref`, but the relationship is not available directly from the Coding Task record.

## 4. Worktree and Docker security boundary

### Worktree protections already present

- Task IDs are restricted to alphanumeric, hyphen and underscore characters.
- Worktrees are created beneath the configured workspace root, on a task-specific branch.
- Branch names are checked with `git check-ref-format`.
- Mutating operations validate the resolved Worktree path against the managed Worktree root and task ID.
- Patch application runs `git apply --check` before `git apply`; patch payloads are capped at 2 MiB.
- Revert resets only the task Worktree and removes untracked files.

### Remaining trust boundaries / hardening requirements

- `repository_path` is currently an arbitrary existing local Git directory supplied in the Coding Task request. The service verifies it is a Git repository, but there is no configured repository-root allowlist or user-confirmed repository capability boundary.
- The Docker container receives the Worktree as a writable host bind mount. Container isolation protects the rest of the host only to the extent that Docker and its daemon are trusted.
- Network isolation defaults to `none`; enabling the existing network option changes it to Docker `bridge`. This must remain an explicit operator decision and be visible in the UI.
- The runner applies read-only container root, dropped capabilities, no-new-privileges, PID/memory/CPU limits and temporary filesystems. It does not currently pin images by digest or set an explicit non-root container UID.
- P6 must not describe Docker as a complete hostile-code security boundary. It is a constrained execution environment whose trust still depends on the host Docker daemon, configured image and mounted repository.

## 5. Patch, test and provenance findings

- The Coding Service saves a final diff and base commit hash in `coding_task_snapshots` on successful completion.
- Test output and Agent/repair output are stored in Coding Task Steps; events record lifecycle milestones and test pass/fail facts.
- The final diff is not currently published as a Collaboration Artifact Version. No Artifact Version is automatically bound to the canonical Execution/Attempt.
- Test output is not automatically represented as P4 Evidence. The existing P4 Artifact Version/Evidence provenance model and Collaboration Artifact service should be reused; P6 must not create a parallel generic artifact table.
- The Coding event table is append-only by current repository usage convention, but migration `000021` does not install database-level update/delete guards. Do not claim database-enforced immutability until that is added and tested.
- The current successful snapshot path does not establish a complete evidence-backed release decision. A diff existing is not proof that tests passed or that a reviewer accepted the change.

## 6. Dashboard findings

The P5 Dashboard's `/dashboard/executions` page can list canonical Coding Executions and show common Attempts, Timeline, Artifact Versions and Evidence. However:

- Dashboard dependencies do not include `CodingService`.
- No Coding Task create/start/cancel/detail routes are registered in the Dashboard.
- Coding Task Steps, task events, repair iterations, test output, Worktree state and final diff are not rendered there.
- The current Dashboard therefore exposes the common execution shell but not the Coding Runtime product workflow.

P6 should add a Coding surface under the existing Dashboard and reuse the current Coding Service and Execution Manager. It must not write Coding or Execution tables directly from templates.

## 7. Existing verification evidence and limits

Current source includes a Git-backed Coding Service test using a fake Agent, plus Worktree diff/patch/reset tests. The historical `scripts/m35-m41-acceptance.sh` runs formatting, sqlc generation, build, race tests and vet.

The repository's P5 full verification was accepted by the user, but that does not prove a real Coding Agent + Docker Sandbox + repair + artifact provenance run. No live Coding acceptance was executed as part of this source audit. P6 must add a separate, explicit live acceptance gate and report any Docker/model prerequisites.

## 8. P6 implementation constraints

1. Reuse the current Coding Service, Coding Task tables, P2/P4 Execution Manager, Workflow Engine, Agent Runtime, Collaboration Artifact/Evidence and P5 Usage Ledger.
2. Do not introduce another general-purpose queue, scheduler, Execution state machine, event ledger or artifact store.
3. Keep Coding repair iterations as Coding Steps under the same runtime Attempt unless a new runtime Attempt is explicitly started.
4. Preserve unknown outcomes. A process restart must never turn an unverified partial code change into a confirmed failure or success.
5. Keep repository access, network-enabled sandbox execution, patch application, revert and cleanup explicit and auditable.
6. Keep all browser-facing assets under repository-root `web/`; Dashboard handlers remain under `internal/transport/http/dashboard/`.
7. Do not automatically commit or push changes. The deliverable is a reviewable Worktree diff plus test/review evidence; the human owns commit/PR approval.
8. No migration by default, but an additive migration is justified for the interrupted-state and durable provenance gaps identified above.
