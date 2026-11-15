# AgentMesh

**AgentMesh is a local-first, Go-based platform for coordinating AI agents and their work.** It brings together model providers, tool execution, persistent Agent and Workflow runtimes, Coding Agent workspaces, and a common execution lifecycle.

- English | [简体中文](README.zh-CN.md)
- Documentation index: [docs/README.md](docs/README.md)
- Unified execution management (P2): [docs/p2-execution.md](docs/p2-execution.md)

## Project capabilities

- **Model access:** provider and model registry, credential handling, routing and a common Model Gateway.
- **Agent runtime:** persistent tasks, tool calling, capability checks, approvals, context and memory.
- **Workflow runtime:** versioned DAGs, parallel branches, checkpoints, event history and recovery.
- **Coding runtime:** isolated Git worktrees, coding-agent execution, sandboxed tests, patch inspection and repair.
- **Unified execution (P2):** one Execution/Attempt lifecycle across Agent, Coding, Workflow and CLI runtimes, without replacing their existing queues.

AgentMesh is a Go modular monolith backed by SQLite. Runtime-specific mechanisms remain in place and are integrated through adapters; P2 does not introduce a second generic job queue.

## Requirements

- Go **1.27.3** (see `go.mod`).
- SQLite through `modernc.org/sqlite`; CGO is not required.
- Docker and Git for sandboxed Coding Agent workflows.
- Optional provider credentials and coding-agent CLIs, depending on the features you use.

## Quick start

```bash
cp configs/config.example.yaml configs/config.yaml
export OPENAI_PRIMARY_API_KEY="your-api-key"
go run ./cmd/agentmesh --config configs/config.yaml
```

The example configuration binds the HTTP server to `127.0.0.1:8080` and stores local data under `./data`. Adjust the configuration for your environment. Provider keys are supplied through environment variables; do not commit real credentials.

Dashboard: open [http://127.0.0.1:8080/dashboard](http://127.0.0.1:8080/dashboard) after startup. When an API Token is configured, sign in with that token; the Dashboard uses a separate short-lived browser session and does not store the token in the cookie. See [P5 Product Control Plane](docs/p5-product-control-plane.md) and the [P5 Part 1 implementation record](docs/p5-part1-implementation.md).

Health endpoints:

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

Keep the service on loopback unless you have explicitly configured and verified an appropriate authentication and network boundary. Do not expose a local development instance directly to the public internet.

## Development and verification

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

For the unified Execution layer:

```bash
bash scripts/verify-p2.sh
```

The P2 script formats Go files before running formatting, build, vet and race-test checks. Use the Go version declared by the repository.

## Documentation

Start with [docs/README.md](docs/README.md) for the documentation map, milestone history and source-of-truth rules. The root Chinese README contains the Chinese project overview. Historical milestone notes are retained as implementation records; their old validation notes describe the environment at the time they were written, not the current branch's CI status.
