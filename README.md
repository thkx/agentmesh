# AgentMesh

AgentMesh is a Go-based multi-provider model gateway and Agent execution platform. The current baseline includes Provider / Model registries, Model Gateway, encrypted credentials, Coding Agent adapters, Tool System, native tool calling, durable Agent scheduling, versioned Workflow DAGs, event history, fencing tokens, and M11 reliability acceptance. M12 adds Goal Management and structured planning; M13 compiles immutable Plans into the existing durable Workflow Runtime.

## Prerequisites

- Go 1.27.3 (`darwin/arm64`)
- sqlc v1.31.1

Install sqlc if needed:

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
sqlc version
```

## Database and SQLC (M1-D)

- SQLite is provided by `modernc.org/sqlite`; no CGO or GCC is required.
- Runtime migrations are embedded from `internal/platform/database/migrations`.
- SQLC schema input points to the same migration `*.up.sql` files.
- Provider and Model catalog synchronization is transactional.
- API keys are not stored in SQLite. `credential_ref` stores only an environment variable name.

Regenerate query code and run validation:

```bash
sqlc generate
go test ./...
go test -race ./internal/platform/database ./internal/integration ./internal/modelgateway
go build ./...
```

## Provider instances and model registry (M1-A / M1-B)

AgentMesh separates a provider vendor type from a configured provider instance:

- `type`: vendor adapter family (`openai`, `anthropic`, `gemini`, `ollama`).
- `id`: a unique AgentMesh instance ID (`openai-primary`, `openai-backup`).
- `models[].provider_id`: references the instance ID, never the vendor type.
- `models[].id`: the stable model ID used by AgentMesh clients.

Copy `configs/config.example.yaml`. Cloud providers may start without credentials and remain registered but unavailable until configured. Configure keys through environment variables (`api_key_env`) or the Credential HTTP API. API key values are not stored in YAML or included in provider JSON output. Each instance gets its own SDK client and Base URL. The OpenAI and Anthropic SDK clients are explicitly configured with their instance endpoint; Gemini receives an explicit `HTTPOptions.BaseURL`; Ollama receives its instance URL and HTTP client.

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

Environment variables are optional. They take precedence over encrypted credentials stored through the HTTP API:

```bash
export OPENAI_PRIMARY_API_KEY="..." # optional
go run ./cmd/agentmesh --config configs/config.yaml
```

The in-memory `ProviderRegistry` and `ModelRegistry` are hydrated from the persisted catalog and exposed by the application container. `ProviderRuntime.ApplyProvider` builds an adapter before publishing an in-memory provider update; `ProviderRuntime.RemoveProvider` removes the in-memory provider instance and adapter. The `RuntimeRepository` provides transactional persistence operations and enforces the provider/model foreign-key relationship. HTTP CRUD endpoints and coordinated persistence of live runtime mutations remain in M1-E.

## Gateway execution policy (M1-C)

The Gateway is the policy boundary between callers and provider adapters:

- Resolves the caller-facing `model_id` to a registered model definition.
- Rejects disabled models and, when constructed with `NewGatewayWithProviders`, missing or disabled provider instances.
- Rejects `max_output_tokens` values above the model's configured `max_output_tokens` ceiling. A zero request value remains provider-default behavior; the Gateway does not silently invent a default.
- Rejects streaming requests when the model does not advertise streaming support.
- Rewrites response, stream-event, and normalized provider-error identities to the resolved AgentMesh provider-instance ID and model ID.
- Normalizes cancellation, deadline, HTTP status, network timeout, and provider errors. Retryable classifications are exposed for rate limits, request timeouts, server failures, and network timeouts; automatic retries are intentionally not performed by the Gateway.
- Stops forwarding a stream after its terminal `completed` or `error` event, and converts unknown event types into normalized stream errors.

The Gateway enforces configured output-token ceilings but does not estimate prompt token usage. Accurate input-token counting requires provider/model-specific tokenizers and is not approximated with character counts.

## M1-D persistence

See [`docs/milestones/M1-D.md`](docs/milestones/M1-D.md) for the migration layout, schema constraints, runtime synchronization flow, and acceptance commands. The root `migrations/` PoC directory has been removed so runtime and SQLC no longer use separate schemas. At startup, YAML entries are upserted transactionally and then the full persisted catalog is loaded; rows not declared in YAML are retained for future management APIs.

## HTTP Runtime (M1-E)

The server starts with `go run ./cmd/agentmesh --config configs/config.yaml` and listens on the configured `server.host:server.port` (default `127.0.0.1:8080`). Liveness/readiness endpoints are `/healthz` and `/readyz`; the versioned API is under `/api/v1`.

Provider and Model catalog endpoints support list/create/get/update/enable-disable/delete. Model execution is available at `POST /api/v1/generate`; streaming uses `POST /api/v1/generate/stream` (SSE). The existing CLI now sends the Gateway request shape to these endpoints.

**Security:** M1-E does not include API authentication. Keep the server bound to loopback; do not expose it to a LAN or public network.

## Milestone M1-F — Integration Tests and Acceptance

M1-F adds regression coverage for same-vendor Provider Instance isolation, failed Adapter replacement safety, Gateway stream cancellation, concurrent catalog writes, and the HTTP Generate/SSE path. See [`docs/milestones/M1-F.md`](docs/milestones/M1-F.md) for the acceptance matrix and local verification commands.

Catalog mutations are serialized within the HTTP handler to prevent concurrent snapshot-based persistence from losing Provider or Model updates. This is an in-process consistency guarantee; multi-process writers are not supported by the current single-process server design.

## M2-A — Credential Domain & Abstraction

M2-A introduces the credential domain and application contracts without changing M1 runtime behavior yet. Credential sources are `environment` and `stored`; safe status values are `unconfigured`, `configured`, and `invalid`. `domain/credential.Secret` keeps the secret value private and redacts standard formatting, while `Record` contains metadata only.

The application contracts are defined in `internal/credential`: `Store`, `EnvironmentLookup`, `Resolver`, and `Manager`. Credential resolution uses Environment > Stored precedence. Missing credentials are represented as an unconfigured provider state and do not prevent startup. `Provider.CredentialRef` / YAML `api_key_env` remains the environment-variable compatibility field. M2-C wires the resolver and encrypted SQLite store into Provider Runtime; an enabled cloud provider without a credential remains registered but has no executable adapter. The credential encryption key is kept outside SQLite at `<database-directory>/keys/credential.key` using restrictive filesystem permissions. Back up this key securely: stored credentials cannot be decrypted if it is lost.

See [`docs/milestones/M2-A.md`](docs/milestones/M2-A.md). Encryption, SQLite credential storage, OS keychain integration, runtime hot reload, and credential HTTP APIs are intentionally deferred to subsequent M2 milestones.

## M2-B — Credential Encryption & Storage

M2-B adds AES-256-GCM authenticated encryption and encrypted SQLite persistence for provider-instance credentials. The encryption key is provided independently from SQLite through `cryptography.KeyProvider`; `FileKeyProvider` is the portable fallback and creates a 32-byte key in a `0600` file inside a `0700` directory. Keep this key file backed up and access-controlled: losing it makes stored credentials unrecoverable. The key file must not be placed inside the SQLite database directory or committed to source control.

`credentialstore.SQLiteStore` implements the M2-A `credential.Store` contract. Stored values are encrypted before persistence using a fresh random GCM nonce; rows carry an encryption version for future format migrations. The `provider_credentials` table references provider instances with `ON DELETE CASCADE`. No HTTP endpoints or runtime adapter reload behavior are introduced in M2-B; those remain M2-D/M2-E.

The file key provider is a portable fallback, not an OS keychain integration. Production deployments should use a platform keychain/KMS-backed implementation of `cryptography.KeyProvider` where available.


### M2-C — Provider Runtime Credential Integration

See [`docs/milestones/M2-C.md`](docs/milestones/M2-C.md) for runtime resolution rules and startup behavior. Missing cloud credentials no longer prevent AgentMesh from starting.

### M2-D — Credential HTTP API

- `GET /api/v1/providers/{id}/credential` — safe credential status metadata.
- `PUT /api/v1/providers/{id}/credential` — set/update `{"api_key":"..."}`.
- `DELETE /api/v1/providers/{id}/credential` — delete the stored credential.

The API never returns the secret. Stored values are encrypted before SQLite persistence. See [`docs/milestones/M2-D.md`](docs/milestones/M2-D.md).

### M2-E — Adapter Hot Reload

Credential changes rebuild and atomically replace the provider-instance adapter without restarting AgentMesh. Requests already using the previous adapter can finish. See [`docs/milestones/M2-E.md`](docs/milestones/M2-E.md).

### M2-F — Provider Connection Testing

`POST /api/v1/providers/{id}/test` performs a bounded upstream generation request using an enabled model. This can incur provider usage/charges. See [`docs/milestones/M2-F.md`](docs/milestones/M2-F.md).

### M2-G / M2-H — Reliability and Acceptance

See [`docs/milestones/M2-G.md`](docs/milestones/M2-G.md) and [`docs/milestones/M2-H.md`](docs/milestones/M2-H.md) for rollback, in-flight request, security, integration test, and acceptance details. The HTTP API has no authentication; keep the listener bound to loopback.


## M3 — Coding Agent Adapter

M3 adds a unified `AgentExecutor` for locally installed Claude Code, Codex CLI, Gemini CLI, and OpenCode. AgentMesh manages process execution, cancellation, timeouts, output normalization, and workspaces; the external coding agents retain their own Agent Loop.

- `POST /api/v1/executions` starts a synchronous coding-agent execution.
- `DELETE /api/v1/executions/:id` cancels an active execution.
- `POST /api/v1/workspaces` creates a private local workspace; `DELETE /api/v1/workspaces/:id` removes it.
- Permission defaults to `read-only`; `workspace-write` must be explicit.
- Configure `agent.workspace_root` and `agent.cli.*` in YAML. The four CLI programs must be installed and authenticated separately.

See [`docs/milestones/M3.md`](docs/milestones/M3.md) for the API contract, adapter command mappings, security model, and acceptance tests. The HTTP API remains unauthenticated and configuration validation now restricts the listener to loopback addresses.


## M4 — Tool System

M4 introduces a standalone Tool System shared by future API Agent Runtime and Coding Agent workflows. `internal/domain/tool` defines tool specifications, JSON argument schemas, permission classes, and stable errors. `internal/tools` provides a thread-safe Registry and an Engine with input/output limits, per-tool timeouts, policy checks, explicit approval gates, workspace context propagation, and audit events.

Built-in tools are registered in `internal/tools/builtin`: `file.read`, `file.write`, `file.list`, `shell.exec`, and `git.run`. File operations reject absolute paths, traversal, and symlink escapes. Writes use a temporary file and atomic rename. `shell.exec` invokes an executable directly without a shell and requires an exact allowlist entry in `agent.tools.allowed_commands`; the default allowlist is empty. `git.run` is restricted to inspection-oriented subcommands. Write and execute permissions require explicit per-invocation approval under the default policy.

HTTP endpoints:
- `GET /api/v1/tools` — list tool schemas and permission metadata.
- `POST /api/v1/tools/{name}/invoke` — invoke a tool with `workspace_id`, `arguments`, and explicit `approved` boolean.
- `GET /api/v1/tools/audit?limit=100` — inspect the bounded in-memory audit trail.

Tool audit is intentionally in-memory in M4 and is lost on restart; durable audit storage is deferred. The HTTP API remains unauthenticated and must stay bound to loopback. Never expose the tool invocation endpoint to an untrusted network. Approval is an explicit local API assertion, not a multi-user approval workflow. See `docs/milestones/M4.md`.

## M5 — Agent Runtime

M5 introduces the application-owned Agent Loop using the existing ModelGateway and Tool Engine. It supports JSON-based tool-call/final control messages, bounded steps and tool calls, repeated-call protection, context character-budget trimming, cancellation, per-task timeouts, and a concurrency limit.

HTTP endpoints:

- `POST /api/v1/agents/run`
- `GET /api/v1/agents/tasks/:id`
- `POST /api/v1/agents/tasks/:id/cancel`

See [`docs/milestones/M5.md`](docs/milestones/M5.md) for the execution protocol, safety boundaries, and limitations.

## M6 — Native Tool Calling & Agent State Persistence

M6 adds provider-neutral `ToolDefinition` / `ToolCall` / `Tool Result` structures to ModelGateway. OpenAI Responses API, Anthropic Messages API, Gemini generateContent, and Ollama Chat implement native function calling. Agent Runtime routes native calls through the existing M4 Tool Engine, preserving policy, approval, timeout and audit behavior.

Migration `000006_create_agent_task_states` stores task/result snapshots and the full conversation transcript in SQLite. The model receives a bounded context window, while persisted history is not truncated. `GET /api/v1/agents/tasks/:id/messages` retrieves persisted messages. Completed and failed tasks remain inspectable after restart; automatic resume of interrupted tasks is intentionally deferred until tool idempotency and durable scheduler ownership are implemented. See [`docs/milestones/M6.md`](docs/milestones/M6.md).

## M7 — Durable Agent Scheduler & Recovery

M7 adds a SQLite-backed asynchronous task queue and worker runtime:

- `POST /api/v1/agents/queue` durably enqueues a task and returns `202 Accepted`.
- `GET /api/v1/agents/jobs/:id` returns queue state, attempts and lease metadata.
- `POST /api/v1/agents/jobs/:id/retry` retries only failed tasks with no recorded tool invocation.
- Worker claims are transactional and protected by owner-bound leases and periodic heartbeats.
- Jobs whose lease expires before execution starts can be requeued. Jobs that expire after execution starts become `interrupted` and are never blindly replayed.
- Model/runtime failures are retried with exponential backoff only while the task has no durable tool invocation intent.
- A SQLite tool invocation ledger persists an intent before each tool side effect and stores completed results. Completed calls are deduplicated; an invocation left in an indeterminate state is refused rather than executed again.
- Existing synchronous `POST /api/v1/agents/run` remains available for compatibility.

Example configuration:

```yaml
scheduler:
  workers: 1
  worker_id: ""
  poll_interval: 500ms
  lease_duration: 30s
  max_attempts: 3
```

SQLite provides durable single-host coordination. Do not put a WAL-mode SQLite database on a network filesystem. HTTP remains loopback-only until authentication and authorization are implemented. See `docs/milestones/M7.md` for delivery semantics and recovery boundaries.

Validation note: M7 scheduler/config sources and test source compilation were checked in an isolated Go 1.23-compatible module; config tests and SQLite migration/recovery SQL smoke checks passed. The actual Go SQLite integration tests and full-project build/tests remain unverified because this environment has Go 1.23.2 while the project requires Go 1.27.3, and toolchain download was unavailable.

## Workflow Graph (M8)

M8 adds versioned Agent Workflows on top of the existing Agent Runtime. Definitions support `agent`, `condition`, `approval`, and `end` nodes, including parallel DAG fan-out/join. Runs are durably enqueued to SQLite-backed Workflow Workers; owner-bound leases, heartbeats, node idempotency records, checkpoint reconciliation, and approval continuation survive process restarts. Independent nodes in a ready graph wave execute concurrently (bounded to 8 per run). Unknown Agent outcomes and possible external side effects are marked indeterminate and are never blindly replayed. See [`docs/milestones/M8.md`](docs/milestones/M8.md) for the API, JSON example, delivery semantics, and recovery limits.

## M9 Workflow observability and operator recovery

M9 adds an append-only per-run event history, reconnectable SSE delivery, node-level execution observations, and audited operator recovery. See [`docs/milestones/M9.md`](docs/milestones/M9.md) for the API contract, recovery safety rules, migration details, and test commands.

New endpoints:

- `GET /api/v1/workflows/runs/:run_id/events?after=0&limit=100`
- `GET /api/v1/workflows/runs/:run_id/events/stream?after=0` (SSE; supports `Last-Event-ID`)
- `GET /api/v1/workflows/runs/:run_id/history`
- `GET /api/v1/workflows/runs/:run_id/nodes`
- `POST /api/v1/workflows/runs/:run_id/nodes/:node_id/recover` (`retry` or `skip`)

Migration `000010_create_workflow_events` is applied automatically by the existing migration runner.

## M10 — Workflow distributed execution boundaries

M10 adds monotonic workflow job fencing tokens. Every claim increments `workflow_jobs.fencing_token`; heartbeat, completion, node-ledger writes and run checkpoints validate the current owner and token. Worker heartbeat renews both the run lease and all running node leases. The SSE endpoint uses bounded pull-based batches (32 events), per-write deadlines and cursor advancement after successful writes, so slow subscribers do not create unbounded in-memory queues. See `docs/milestones/M10.md` for guarantees, tests and operational limits.

Migration: `000011_workflow_fencing`.

## M11 — Reliability acceptance

M11 adds acceptance tests only (no runtime behavior or schema changes): four independent OS processes contend for one Workflow job, the lease owner is killed and recovered, stale fencing writes are rejected, ambiguous third-party tool calls are not replayed, provider idempotency keys are exercised, and a long-running node is held beyond its original lease while heartbeat renewal is verified. Run `./scripts/reliability/m11.sh` with the Go version required by `go.mod`. See `docs/milestones/M11.md` for test guarantees and deployment caveats.


## M12 — Goal & Planning Core

M12 introduces structured Goals, success criteria, a provider-independent Planner contract, an LLM Planner backed by Model Gateway, deterministic Plan validation, immutable per-goal Plan versions, SQLite persistence, and Goal/Plan HTTP APIs. Plans are validated and persisted but are not executed in M12; Plan-to-Workflow compilation is deferred to M13. See [`docs/milestones/M12.md`](docs/milestones/M12.md) for schema, API routes, lifecycle rules, and verification commands.

### M13 — Plan → Workflow Compiler

M13 compiles immutable M12 Goal Plans into the existing durable Workflow DAG and submits them through the M8–M11 runtime. It adds Plan-version execution reservations and deterministic run IDs, condition/approval/agent/end node mapping, explicit Agent Profile bindings, and multi-root support through a pure Workflow start node. It does not add a second queue or claim per-node retries or exactly-once external effects. See [`docs/milestones/M13.md`](docs/milestones/M13.md).

### M14 — Goal Outcome Evaluation & Completion

M14 closes the Goal lifecycle by evaluating terminal Workflow Runs against declared success criteria. It collects successful node outputs, requires exact evidence quotes tied to real node IDs, stores append-only per-run evaluations, and atomically completes a Goal only when the Workflow Run succeeded and every required criterion is satisfied. Failed Workflow Runs cannot complete a Goal. See [`docs/milestones/M14.md`](docs/milestones/M14.md).

New endpoints:

- `POST /api/v1/goals/:id/evaluations` with `{"run_id":"...","model_id":"..."}`
- `GET /api/v1/goals/:id/evaluations`
- `GET /api/v1/goals/:id/evaluations/:run_id`

Migration: `000014_goal_evaluations`.


### M15 — Reflection & Evidence-Grounded Learning

M15 adds a reflection layer after M14 Goal Evaluation. It reviews the Goal, immutable evaluation, and workflow node observations to extract success patterns, failure causes, improvement opportunities, and risks. Every insight must cite an exact quote from a real node output or error; references are validated before persistence. Reflection records are append-only and idempotent per Goal/Run. M15 does not automatically modify Plans, tool policies, Agent Profiles, or long-term memory. See `docs/milestones/M15.md`.

New endpoints:

- `POST /api/v1/goals/:id/reflections` with `{"run_id":"...","model_id":"..."}`
- `GET /api/v1/goals/:id/reflections`
- `GET /api/v1/goals/:id/reflections/:run_id`

Migration: `000015_goal_reflections`.

## M16 — Memory System

M16 adds evidence-grounded Episodic, Semantic, and Procedural memory. M15 Reflection insights enter as non-retrievable candidates; explicit promotion is required before a memory can be retrieved. Candidates can be rejected, and active memories can be archived. SQLite keeps source provenance, lifecycle transitions, and immutable source content. Retrieval uses deterministic lexical matching (no vector database dependency) and injects a bounded, clearly labeled memory context into newly created M13 Workflow Runs. Agent-scoped memories are selected per Agent Profile at prompt-render time. Migrations `000016_memory_system` and `000017_memory_source_integrity` enforce lifecycle and source provenance. See `docs/milestones/M16.md` for APIs, lifecycle rules, verification, and limitations.
