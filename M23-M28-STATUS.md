# AgentMesh M23–M28 implementation status

This release extends the M17–M22 baseline without changing the existing Provider/Model/Router or Workflow schemas.

## M23 — Agent Definition

- Added `agent_definitions` and immutable version snapshots (`agent_definition_versions`).
- Added definition Domain validation, SQLite Repository, Service and HTTP CRUD endpoints.
- Definition runs now apply the saved system prompt, model ID, temperature, step/tool/token/time budgets and approved tool names.

## M24 — Agent Lifecycle

- Added durable `agent_runs` records and run history endpoints.
- Existing runtime snapshots, scheduler leases, retry/recovery rules and cancellation remain the execution lifecycle foundation.
- Added in-process pause/resume controls for active runtime tasks. Pause takes effect at model/tool boundaries; it cannot interrupt an in-flight provider request or a running tool.
- A process restart does not resume an arbitrary paused run automatically. Existing scheduler recovery intentionally avoids replaying potentially side-effecting work.

## M25 — Agent Execution Engine

- Reuses the existing multi-step Agent Runtime, Context Manager, Loop Guard, Model Gateway and idempotent Tool Ledger.
- Agent definitions can set system prompt and model temperature in addition to budgets.

## M26 — Tool Registry

- Reuses the concurrency-safe Tool Registry, JSON argument validation, dispatcher and provider-native Function Calling integration.

## M27 — Built-in Tools

- File read/write/list tools remain workspace-confined.
- Shell execution now requires an explicitly configured Docker sandbox image and executable allowlist; commands are invoked directly, never through a shell string.
- Added fixed Go test/build/vet/fmt tools when a Docker sandbox image is configured.
- Added HTTPS request tool with explicit domain allowlist, public-IP checks, redirects disabled and bounded response size.
- `git.run` remains restricted to a fixed set of inspection-only subcommands.

## M28 — Security and Reliability

- Added persistent SQLite tool audit records.
- Audit persistence failures are returned to the caller rather than silently discarded.
- Existing read/write/execute policy and explicit approval requirements remain in force.
- Docker runner adds no-new-privileges, drops Linux capabilities, and limits PIDs, memory and CPU.

## HTTP endpoints

- `GET/POST /api/v1/agent-definitions`
- `GET/PUT/DELETE /api/v1/agent-definitions/:id`
- `POST /api/v1/agent-definitions/:id/run`
- `GET /api/v1/agent-definitions/:id/runs`
- `GET /api/v1/agent-runs/:run_id`
- Existing `POST /api/v1/agents/tasks/:id/cancel`
- New `POST /api/v1/agents/tasks/:id/pause` and `/resume`
- Existing `GET /api/v1/tools`, `POST /api/v1/tools/:name/invoke`, `GET /api/v1/tools/audit`

## Configuration

`agent.tools.sandbox_image` must be set before Shell and Go tools can execute. `agent.tools.allowed_commands` is an explicit executable allowlist. `agent.tools.allowed_http_domains` controls the HTTPS tool and is empty by default (fail closed). Docker networking is disabled by default; only set `sandbox_network_enabled: true` if the task needs network access.

## Validation

- All 19 SQL migrations were applied to an in-memory SQLite database using Python's SQLite engine; M23–M28 table smoke tests passed.
- Agent Domain and Agent Catalog packages passed `go test -race` in an isolated Go 1.23.2 module.
- Agent Runtime tests, including pause/resume, passed `go test -race` in an isolated module using minimal model-gateway/workspace test doubles.
- Tool Registry and built-in tool tests passed `go test -race` in an isolated Go 1.23.2 module.
- New Agent Definition HTTP handler type-checked against a minimal Gin API stub; this is not a substitute for compiling against the actual Gin dependency.
- `gofmt` was applied to changed Go files.
- Full project `go test -race ./...`, `go vet ./...`, `go build ./...` and `sqlc generate` were not run because this environment has Go 1.23.2 while the project requires Go 1.27.3 and dependency downloads are unavailable. Do not treat this archive as production-verified until those commands pass on the project's pinned toolchain.
