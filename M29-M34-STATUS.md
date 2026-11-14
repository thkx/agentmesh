> Historical implementation record: this file records the state and validation evidence at the time it was written. Its validation section is not the current branch's verification status. For current project status and documentation navigation, see [the documentation index](docs/README.md) and the current [P2 execution guide](docs/p2-execution.md).

# AgentMesh M29–M34 Implementation Status

This is an incremental continuation of the M23–M28 source tree. Existing Provider, Model Router, Workflow Runtime, scheduler, checkpoint, M1–M28 migrations and package boundaries are retained. YAML remains the canonical workflow definition; the visual editor is an authoring view over the same definition.

## M29 — Conversation Context

- Added SQLite-backed conversations, ordered messages, archival, metadata and summary ranges.
- Added conversation CRUD/list/message/archive HTTP endpoints.
- Message records retain tool-call JSON and tool-call IDs so tool interactions can be reconstructed.
- Added migration `000020_context_and_workflow_authoring`.

## M30 — Short-term / Long-term / Working Memory

- Conversation history is the persisted short-term context.
- Existing M16 memory lifecycle remains the long-term memory store (candidate → active → archived/rejected, scoped to goal/agent/global).
- Context assembly can retrieve approved active memories through the existing Memory Service; it does not bypass the promotion lifecycle.
- Per-run working context remains isolated in the existing Agent/Workflow run state.

## M31 — Context Intelligence

- Added a conservative mixed-language token estimator, explicit model-window/output-reserve/safety-margin budget, and budget-aware message fitting.
- Added model-gateway-powered conversation summarization with summary range persistence.
- Added `/api/v1/conversations/:id/context` for memory retrieval, summary inclusion, optional compression and budget fitting.
- Agent Runtime can enforce `agent.runtime.max_context_tokens`; zero preserves the prior character-only behavior. Existing character trimming remains as a first bound.
- Provider tokenizers are not available uniformly; estimates are deliberately conservative and provider-reported usage remains authoritative.

## M32 — YAML Workflow Definition + Visual Editing

- Added YAML import/export with compile-time graph validation.
- Added Graph DTO conversion and graph GET/PUT APIs.
- Added embedded browser editor at `/workflow-editor`, with node placement, node configuration, edge creation, graph save, YAML import/export.
- The editor calls the existing Workflow Engine; it does not introduce a second workflow runtime.

## M33 — Conditions, Parallelism and Dynamic Routing

- Existing runtime already executes independent ready DAG nodes concurrently (bounded to eight in-process node workers) and has condition branches.
- Extended condition operators: `equals`, `not_equals`, `exists`, `not_exists`, `contains`, `gt`, `gte`, `lt`, `lte`.
- Existing edge handles `true` / `false` remain compatible. Independent DAG branches continue to use existing checkpoint/ledger/fencing behavior.

## M34 — Sub-workflows, Human Approval, Templates and Versions

- Added `subworkflow` node support with explicit workflow ID, optional static input or parent-context input, stable child run ID, and nesting limit of eight.
- Sub-workflows execute synchronously inside their parent node and must finish successfully; a child workflow that pauses for human approval is currently surfaced as a failed parent node rather than being transparently bubbled up. Parent/child approval propagation is a future extension.
- Existing approval nodes and approve/reject APIs remain the human approval mechanism.
- Existing immutable workflow versions are exposed through `/api/v1/workflows/:id/versions`.
- Added persistent workflow templates and template CRUD APIs.

## Added API

- `POST /api/v1/conversations`
- `GET /api/v1/conversations`
- `GET /api/v1/conversations/:id`
- `POST /api/v1/conversations/:id/messages`
- `GET /api/v1/conversations/:id/messages`
- `POST /api/v1/conversations/:id/archive`
- `POST /api/v1/conversations/:id/context`
- `GET /api/v1/workflows/:id/yaml`, `PUT /api/v1/workflows/:id/yaml`
- `GET /api/v1/workflows/:id/graph`, `PUT /api/v1/workflows/:id/graph`
- `GET /api/v1/workflows/:id/versions`
- `POST/GET /api/v1/workflow-templates`, `GET/DELETE /api/v1/workflow-templates/:id`, `POST /api/v1/workflow-templates/:id/apply`
- `GET /workflow-editor`

## Validation

- `gofmt -l .` is clean.
- All SQL migrations `000001` through `000020` were applied in order using Python's SQLite engine; new conversation, message, summary and workflow-template tables passed insert smoke tests.
- Full `go build ./...`, `go test -race ./...`, `go vet ./...` and `sqlc generate` were **not run**: this build environment has Go 1.23.2 while this project requires Go 1.27.3, and dependency downloads are unavailable. Do not treat this ZIP as having passed full Go compilation until the commands are run in the project's Go 1.27.3 environment.

## Known limitations

- Token estimates are heuristic, not exact provider tokenizer counts.
- Sub-workflow approval propagation is not yet implemented; child workflows should not contain approval barriers when invoked as sub-workflow nodes.
- Visual editor is a lightweight SVG editor; it intentionally avoids replacing the established Go/HTMX-oriented application architecture with a separate SPA.
- Workflow templates are stored independently; applying a template should create a new workflow version via the existing workflow definition API.
