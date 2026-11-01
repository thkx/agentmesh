# AgentMesh

[English](README.md) | [简体中文](README.zh-CN.md)

**AgentMesh** is a Go-based, multi-provider model gateway and agent execution runtime. It provides a unified interface for model providers, a policy-enforcing Model Gateway, local coding-agent process adapters, a reusable Tool System, durable Agent scheduling, and versioned Workflow execution.

The project is developed in milestones. This README summarizes the capabilities and operational boundaries documented for **M1–M11**. Detailed design decisions, API contracts, migrations, and acceptance commands are maintained in the milestone documents under [`docs/milestones/`](docs/milestones/).

## Contents

- [Capabilities](#capabilities)
- [Architecture at a glance](#architecture-at-a-glance)
- [Requirements](#requirements)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [Model providers and Gateway](#model-providers-and-gateway)
- [HTTP API overview](#http-api-overview)
- [Security and operational boundaries](#security-and-operational-boundaries)
- [Database and migrations](#database-and-migrations)
- [Milestone status](#milestone-status)
- [Development and verification](#development-and-verification)
- [Documentation](#documentation)

## Capabilities

| Area | Capability |
|---|---|
| Model providers | OpenAI, Anthropic, Gemini, and Ollama adapters |
| Provider instances | Multiple independently configured instances of the same provider type |
| Model Gateway | Model resolution, capability checks, output-token limits, normalized errors and stream events |
| Credential management | Environment/stored credential resolution, encrypted SQLite storage, safe status APIs, adapter hot reload |
| Coding agents | Process adapters for Claude Code, Codex CLI, Gemini CLI, and OpenCode |
| Tool System | Registry, execution engine, permission checks, approval gates, bounded inputs/outputs, built-in tools |
| Agent Runtime | Native tool calling, bounded Agent Loop, cancellation, task state and message persistence |
| Durable scheduler | SQLite-backed jobs, owner-bound leases, heartbeats, retry boundaries, tool invocation ledger |
| Workflow Runtime | Versioned DAG workflows, parallel fan-out/join, approval nodes, durable checkpoints |
| Observability | Workflow event history, SSE event stream, node execution history and operator recovery |
| Reliability | Fencing tokens, stale-worker write rejection, bounded SSE delivery, multi-process and crash acceptance tests |

## Architecture at a glance

```text
                 ┌────────────────────────────┐
                 │        HTTP / CLI          │
                 └─────────────┬──────────────┘
                               │
             ┌─────────────────▼─────────────────┐
             │          Application API           │
             └──────────┬───────────────┬─────────┘
                        │               │
              ┌─────────▼──────┐  ┌─────▼──────────┐
              │  Model Gateway │  │ Agent / Workflow│
              └─────────┬──────┘  │    Runtime      │
                        │         └─────┬──────────┘
              ┌─────────▼──────┐        │
              │ Provider Runtime│  ┌────▼──────────┐
              │ + Adapter Factory│  │  Tool Engine  │
              └─────────┬──────┘  └────┬──────────┘
                        │              │
          ┌─────────────▼──────┐   ┌───▼────────────┐
          │ OpenAI / Anthropic │   │ Built-in Tools │
          │ Gemini / Ollama    │   │ + Tool Registry│
          └────────────────────┘   └────────────────┘

                 SQLite persistence
       catalog · credentials · tasks · jobs
       tool ledger · workflow runs · checkpoints
       workflow events · execution history
```

The Model Gateway owns provider-neutral model execution policy. Agent and Workflow runtimes build on that gateway and the shared Tool Engine. SQLite provides local persistence and coordination; it is not presented as a multi-host distributed database.

## Requirements

- Go **1.27.3**, matching the version currently documented for this project.
- [`sqlc`](https://sqlc.dev/) **v1.31.1** for regenerating database query code.
- SQLite is provided by `modernc.org/sqlite`; **CGO and GCC are not required**.

Install sqlc:

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
sqlc version
```

## Quick start

1. Clone the repository and enter the project directory.
2. Copy the example configuration.
3. Set any provider credentials required by the models you intend to call.
4. Start AgentMesh.

```bash
cp configs/config.example.yaml configs/config.yaml

# Optional: configure a provider credential through its environment variable.
export OPENAI_PRIMARY_API_KEY="your-api-key"

go run ./cmd/agentmesh --config configs/config.yaml
```

The server listens on the configured `server.host` and `server.port`; the documented default is `127.0.0.1:8080`.

Health checks:

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

The exact configuration fields and API request/response contracts are described in the relevant milestone documents. Keep the listener on loopback: the HTTP API does not currently provide authentication and authorization.

## Configuration

The example configuration is [`configs/config.example.yaml`](configs/config.example.yaml). Copy it to `configs/config.yaml` and adjust it for the local environment.

### Provider and model identity

AgentMesh distinguishes a provider vendor type from a configured provider instance:

- `providers[].type` identifies the adapter family: `openai`, `anthropic`, `gemini`, or `ollama`.
- `providers[].id` identifies one configured instance, such as `openai-primary` or `openai-backup`.
- `models[].provider_id` references the provider **instance ID**, not the vendor type.
- `models[].id` is the stable model ID presented to AgentMesh clients.

Example:

```yaml
providers:
  - id: openai-primary
    name: OpenAI Primary
    type: openai
    kind: cloud
    base_url: https://api.openai.com/v1
    api_key_env: OPENAI_PRIMARY_API_KEY
    enabled: true

  - id: openai-backup
    name: OpenAI Backup
    type: openai
    kind: cloud
    base_url: https://gateway.example.com/v1
    api_key_env: OPENAI_BACKUP_API_KEY
    enabled: true

models:
  - id: assistant-fast
    provider_id: openai-primary
    name: gpt-4.1-mini
    supports_streaming: true
    enabled: true
```

Each provider instance has its own adapter client and endpoint configuration. This allows two instances of the same vendor type to use different Base URLs and credentials.

Cloud providers can remain registered when credentials are missing, but they are not executable until a credential is available. Environment credentials take precedence over encrypted stored credentials. Secret values are not returned by provider or credential status APIs.

### Credential key management

Stored credentials are encrypted with AES-256-GCM. The portable `FileKeyProvider` stores a 32-byte key in a file with restrictive permissions (`0600` file inside a `0700` directory).

- Keep the key outside the SQLite database directory.
- Do not commit the key to source control.
- Back it up securely. If the key is lost, stored credentials cannot be decrypted.
- The file provider is a portable fallback, not an OS keychain or KMS integration.

See [`M2-A`](docs/milestones/M2-A.md), [`M2-B`](docs/milestones/M2-B.md), and [`M2-C`](docs/milestones/M2-C.md).

## Model providers and Gateway

The Gateway is the policy boundary between callers and provider adapters. It:

- Resolves the caller's `model_id` to a registered model definition.
- Rejects disabled models and, when provider validation is enabled, missing or disabled provider instances.
- Enforces the model's configured `max_output_tokens` ceiling. A request value of zero retains provider-default behavior.
- Rejects streaming requests for models that do not advertise streaming support.
- Normalizes response, stream-event, and provider-error identities to AgentMesh provider-instance and model IDs.
- Normalizes cancellation, deadlines, HTTP status errors, network timeouts, and provider errors.
- Exposes retryable error classifications for rate limits, request timeouts, server failures, and network timeouts. It does **not** automatically retry requests.
- Stops forwarding after terminal `completed` or `error` stream events and normalizes unknown event types as stream errors.

The Gateway does not estimate prompt token usage. Accurate input-token counting requires provider/model-specific tokenizers; character-count approximations are intentionally avoided.

## HTTP API overview

The API is versioned under `/api/v1`. This is a capability index, not a replacement for the detailed contracts in the milestone documents.

| Area | Endpoint(s) | Purpose |
|---|---|---|
| Health | `GET /healthz`, `GET /readyz` | Liveness and readiness |
| Model execution | `POST /api/v1/generate`, `POST /api/v1/generate/stream` | Non-streaming and SSE generation |
| Provider credentials | `GET/PUT/DELETE /api/v1/providers/{id}/credential` | Read safe status, set, or delete a stored credential |
| Provider connection | `POST /api/v1/providers/{id}/test` | Bounded upstream connection test; may incur usage charges |
| Coding agent | `POST /api/v1/executions`, `DELETE /api/v1/executions/{id}` | Start/cancel a coding-agent execution |
| Workspace | `POST /api/v1/workspaces`, `DELETE /api/v1/workspaces/{id}` | Create/remove a local workspace |
| Tools | `GET /api/v1/tools`, `POST /api/v1/tools/{name}/invoke` | List and invoke tools |
| Tool audit | `GET /api/v1/tools/audit?limit=100` | Inspect the bounded in-memory audit trail |
| Agent Runtime | `POST /api/v1/agents/run` | Run an Agent task synchronously |
| Agent task | `GET /api/v1/agents/tasks/{id}`, `POST /api/v1/agents/tasks/{id}/cancel` | Inspect/cancel a task |
| Agent messages | `GET /api/v1/agents/tasks/{id}/messages` | Read persisted conversation messages |
| Agent queue | `POST /api/v1/agents/queue` | Enqueue an asynchronous task |
| Agent job | `GET /api/v1/agents/jobs/{id}`, `POST /api/v1/agents/jobs/{id}/retry` | Inspect/retry an eligible failed job |
| Workflow definitions | `PUT/GET /api/v1/workflows/{id}` | Create/update and retrieve a versioned workflow definition |
| Workflow runs | `POST /api/v1/workflows/{id}/runs`, `GET /api/v1/workflows/{id}/runs` | Start/list workflow runs |
| Workflow run detail | `GET /api/v1/workflows/runs/{run_id}` | Inspect a workflow run |
| Workflow approval | `POST /api/v1/workflows/runs/{run_id}/approve`, `POST /api/v1/workflows/runs/{run_id}/reject` | Continue or reject an approval node |
| Workflow events | `GET /api/v1/workflows/runs/{run_id}/events` | Read persisted run events |
| Workflow event stream | `GET /api/v1/workflows/runs/{run_id}/events/stream` | Reconnectable SSE event stream |
| Workflow history | `GET /api/v1/workflows/runs/{run_id}/history`, `GET /api/v1/workflows/runs/{run_id}/nodes` | Inspect run and node execution history |
| Operator recovery | `POST /api/v1/workflows/runs/{run_id}/nodes/{node_id}/recover` | Audited node retry/skip operation |

For exact payloads, status codes, validation rules, and safety conditions, use the corresponding milestone documentation.

## Security and operational boundaries

These constraints are important when running AgentMesh:

1. **No HTTP authentication/authorization:** keep the server bound to loopback. Do not expose it to a LAN or public network.
2. **Tool execution is privileged:** `shell.exec` requires an exact executable allowlist entry; the default allowlist is empty. Write and execute permissions require explicit per-invocation approval under the default policy.
3. **Workspace isolation matters:** built-in file tools reject absolute paths, path traversal, and symlink escapes. Do not treat this as a substitute for OS-level sandboxing.
4. **SQLite is single-host coordination:** do not place a WAL-mode database on a network filesystem or treat it as a multi-host distributed queue.
5. **At-least-once boundaries:** durable intent and invocation ledgers prevent blind replay in ambiguous cases, but cannot guarantee exactly-once effects in arbitrary third-party systems.
6. **Workflow recovery is guarded:** unknown outcomes and potentially completed external side effects are not blindly replayed. Operator recovery is constrained and audited.
7. **Credential encryption depends on key custody:** protect and back up the encryption key independently of the database.
8. **Connection tests can cost money:** provider test requests may consume upstream tokens or incur charges.

## Database and migrations

- SQLite uses `modernc.org/sqlite`; CGO/GCC are not needed.
- Runtime migrations are embedded from `internal/platform/database/migrations`.
- SQLC schema input uses the same migration `*.up.sql` files.
- Provider and Model catalog synchronization is transactional.
- Provider credentials are encrypted before being persisted.
- Agent tasks, scheduler jobs, tool invocation records, workflow checkpoints, and workflow events are persisted in SQLite.

The root-level PoC `migrations/` directory has been removed; runtime and SQLC use the same migration source. At startup, YAML catalog entries are upserted transactionally and the persisted catalog is then loaded. Database rows not declared in YAML are retained for management workflows.

Regenerate SQLC code:

```bash
sqlc generate
```

See [`M1-D`](docs/milestones/M1-D.md) for schema and catalog synchronization details.

## Milestone status

The following summarizes the implementation described by this repository's milestone notes.

| Milestone | Scope | Document |
|---|---|---|
| M1-A | Provider and Model registries | — |
| M1-B | Isolated provider runtime and adapter factory | — |
| M1-C | Gateway execution policy | — |
| M1-D | SQLite persistence for Provider/Model catalog | [M1-D](docs/milestones/M1-D.md) |
| M1-E | HTTP runtime and catalog/generation APIs | — |
| M1-F | Integration tests and acceptance coverage | [M1-F](docs/milestones/M1-F.md) |
| M2-A–M2-C | Credential domain, encryption/storage, runtime integration | [M2-A](docs/milestones/M2-A.md) · [M2-B](docs/milestones/M2-B.md) · [M2-C](docs/milestones/M2-C.md) |
| M2-D–M2-F | Credential API, adapter hot reload, provider connection testing | [M2-D](docs/milestones/M2-D.md) · [M2-E](docs/milestones/M2-E.md) · [M2-F](docs/milestones/M2-F.md) |
| M2-G–M2-H | Reliability and acceptance | [M2-G](docs/milestones/M2-G.md) · [M2-H](docs/milestones/M2-H.md) |
| M3 | Coding Agent adapters and workspaces | [M3](docs/milestones/M3.md) |
| M4 | Tool System and built-in tools | [M4](docs/milestones/M4.md) |
| M5 | Agent Runtime | [M5](docs/milestones/M5.md) |
| M6 | Native tool calling and Agent state persistence | [M6](docs/milestones/M6.md) |
| M7 | Durable Agent scheduler and recovery | [M7](docs/milestones/M7.md) |
| M8 | Versioned Workflow Graph Runtime | [M8](docs/milestones/M8.md) |
| M9 | Workflow events, observability, and operator recovery | [M9](docs/milestones/M9.md) |
| M10 | Workflow fencing and bounded SSE delivery | [M10](docs/milestones/M10.md) |
| M11 | Reliability acceptance tests | [M11](docs/milestones/M11.md) |

### Reliability acceptance

M11 adds acceptance tests without changing runtime behavior or the database schema. The documented scenarios include:

- Four independent OS processes contending for one Workflow job.
- Killing a lease-owning worker and verifying recovery.
- Rejecting stale writes using fencing tokens.
- Refusing to replay ambiguous third-party tool calls.
- Exercising provider idempotency keys.
- Verifying heartbeat renewal during a long-running node.

Run the acceptance script with the Go version required by `go.mod`:

```bash
./scripts/reliability/m11.sh
```

See [`M11`](docs/milestones/M11.md) for the acceptance scope and deployment caveats. A passing acceptance suite is evidence for those tested scenarios; it is not a guarantee of exactly-once external side effects or unlimited production scale.

## Development and verification

Common local checks:

```bash
# Regenerate generated database code after query/schema changes.
sqlc generate

# Run all package tests.
go test ./...

# Run race checks on the key persistence and execution packages.
go test -race ./internal/platform/database ./internal/integration ./internal/modelgateway

# Static analysis.
go vet ./...

# Build all packages.
go build ./...
```

Run the M11 reliability acceptance script separately:

```bash
./scripts/reliability/m11.sh
```

Use the milestone document for the acceptance commands relevant to a specific change. Do not interpret a source-compilation check or SQL smoke test as a substitute for the full integration test suite.

## Documentation

- [`docs/milestones/`](docs/milestones/) — milestone design, API contracts, implementation notes, and acceptance criteria.
- [`configs/config.example.yaml`](configs/config.example.yaml) — example runtime configuration.
- [`scripts/reliability/m11.sh`](scripts/reliability/m11.sh) — M11 reliability acceptance script.

---

**Project status:** M1–M11 capabilities and their boundaries are documented above. Consult the milestone notes and the current source tree before relying on any endpoint or operational behavior in a deployment.
