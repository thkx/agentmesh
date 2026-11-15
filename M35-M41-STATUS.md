> Historical implementation record: this file records the state and validation evidence at the time it was written. Its validation section is not the current branch's verification status. For current project status and documentation navigation, see [the documentation index](docs/README.md) and the current [P2 execution guide](docs/p2-execution.md).

# AgentMesh M35–M41 Implementation

This release is an additive continuation of the uploaded M1–M34 project. Existing package boundaries, provider/model catalog, Agent Runtime, Tool Registry, workspace manager and migrations 000001–000020 are retained.

## M35 — Coding Task Domain & Lifecycle
- Added SQLite-backed coding task, step, event and snapshot records (`000021`).
- Added validated task creation, status lifecycle, bounded timeout and repair attempts.
- Added task list/detail, step/event history, cancellation and startup recovery of interrupted runs.
- A process restart marks in-flight tasks failed and deliberately retains their worktrees for inspection.

## M36 — Git Worktree Workspace
- Every task receives a separate Git worktree and branch (`agentmesh/<task-id>`).
- Worktrees are created under the configured Agent workspace root, which is already managed by the existing Workspace Manager.
- Cleanup removes the worktree and its task branch; active tasks cannot be cleaned up.

## M37 — Coding Agent Runtime
- Reuses the existing Agent Runtime, model gateway, tool registry, tool policy and tool audit.
- Coding instructions are scoped to the task's worktree ID; each task and repair attempt has an isolated Agent Runtime task ID.
- The existing runtime's step/tool/context budgets and execution timeout remain authoritative.

## M38 — Code Change & Patch Management
- Exposes binary-safe Git diff retrieval.
- Patch application uses `git apply --check` before applying and caps payload size at 2 MiB.
- Revert resets only the task worktree to its starting HEAD and removes untracked files.
- Final diff and base commit hash are persisted as a task snapshot.

## M39 — Docker Sandbox
- Test commands run through the existing Docker Runner, not through a host shell.
- Command executable allowlist: go, npm, pnpm, yarn, pytest, python, cargo and make.
- Shell metacharacters are rejected; arguments are passed as argv, never interpolated into a shell command.
- Docker defaults to network isolation, read-only container root, dropped capabilities, no-new-privileges, PID/memory/CPU limits and a writable task-worktree mount.
- Configure `agent.tools.sandbox_image` to an image containing the toolchain and cached dependencies required by the repository. Network can be explicitly enabled in config when dependency download is required.

## M40 — Test, Review & Self-Repair
- Detects Go, Node, Python and Rust project test commands when no explicit command is supplied.
- Captures test output and exit status in task result/events.
- On test failure, asks the existing Agent Runtime to repair the code, then reruns tests up to `max_repair_attempts` (0–5).
- Tests are never automatically deleted or weakened by the orchestration layer.

## M41 — Integration & Reliability
- Added asynchronous HTTP endpoints under `/api/v1/coding/tasks`; `POST .../:id/run` returns `202 Accepted` and clients poll task state.
- Added CLI commands: `agentmesh-cli coding create`, `run`, and `list`.
- Added task concurrency bound (two in-process coding runs), cancellation, startup recovery, event history and workspace cleanup.
- CLI automatically sends `AGENTMESH_API_TOKEN` as a Bearer token when configured.

## API

- `POST /api/v1/coding/tasks`
- `GET /api/v1/coding/tasks`
- `GET /api/v1/coding/tasks/:id`
- `POST /api/v1/coding/tasks/:id/run`
- `POST /api/v1/coding/tasks/:id/cancel`
- `GET /api/v1/coding/tasks/:id/steps`
- `GET /api/v1/coding/tasks/:id/events`
- `GET /api/v1/coding/tasks/:id/snapshots`
- `GET /api/v1/coding/tasks/:id/diff`
- `POST /api/v1/coding/tasks/:id/patch` (`{"patch":"..."}`)
- `POST /api/v1/coding/tasks/:id/revert`
- `DELETE /api/v1/coding/tasks/:id/workspace`

Create a task with:

```json
{
  "id": "fix-login-bug",
  "title": "Fix login regression",
  "repository_path": "/absolute/path/to/repository",
  "model_id": "your-model-id",
  "prompt": "Find and fix the login bug. Add regression tests.",
  "test_command": {"program": "go", "args": ["test", "./..."]},
  "max_repair_attempts": 2,
  "timeout_seconds": 1800
}
```

`POST /api/v1/coding/tasks/:id/run` returns `202 Accepted`; poll `GET /api/v1/coding/tasks/:id` until status is `succeeded`, `failed`, or `canceled`.

## Run

1. Apply migrations by starting AgentMesh normally.
2. Configure `agent.workspace_root` and `agent.tools.sandbox_image`.
3. Ensure Docker and Git are installed.
4. Create and execute a task:

```bash
agentmesh-cli coding create \
  --id fix-login-bug \
  --repo /path/to/repository \
  --model your-model-id \
  --prompt "Find and fix the login bug. Add regression tests."
agentmesh-cli coding run fix-login-bug --timeout 30m
agentmesh-cli coding list
```

5. Inspect the resulting patch with `GET /api/v1/coding/tasks/fix-login-bug/diff`, then clean up when finished.

## Validation status

The uploaded source tree was inspected and the M35–M41 implementation was added.

- `gofmt -l .`: passed (no output).
- SQLite smoke validation: all migrations `000001`–`000021` applied successfully; migration `000021` down migration removed the four coding tables successfully.
- Git worktree / untracked-file diff / `git apply --check` / reset sequence: smoke checked.
- Full Go build and tests: not verified here. The available environment is Go 1.23.2 while this project declares Go 1.27.3. `go test ./internal/coding` attempted to download Go 1.27.3, but the toolchain download failed because DNS/network access to `proxy.golang.org` is unavailable.
- `go test -race ./...`, `go build ./...`, `go vet ./...` and `sqlc generate` therefore remain unverified. Run `scripts/m35-m41-acceptance.sh` using Go 1.27.3 and sqlc on the target machine.

## Known constraints

- Coding execution runs in an in-process background goroutine after the HTTP request is accepted. Tasks are persisted before execution; restart recovery marks interrupted tasks failed and retains worktrees.
- Startup recovery marks interrupted tasks failed and preserves their worktrees; it does not automatically resume a possibly partially-applied edit.
- Test execution depends on the configured Docker image having the required language toolchain and dependencies. With network disabled, dependencies must already be cached in the image.
- The coding agent reuses the existing Agent Runtime and its approved-tool policy. `file.write` is approved for the isolated task worktree; shell execution remains disabled unless explicitly allowlisted in config.
- Coding runs are still bounded by `agent.runtime.max_steps` and `agent.runtime.max_tool_calls`. For larger refactors, increase these global ceilings (for example 80 steps / 200 tool calls); the task cannot exceed the configured Runtime ceiling.
