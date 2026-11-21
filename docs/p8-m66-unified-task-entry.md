# P8 M66 — Unified Task Entry

**Branch:** `p8/m66-unified-task-entry`
**Base:** `p7/product-onboarding`
**Product outcome:** a first-time user can start one of the existing Agent, Coding, or Workflow execution types from one understandable entry point without learning the internal navigation first.

## User experience

The Overview page exposes one primary action: **Start a task**. It opens `/dashboard/new-task`, a single launcher with three plain-language choices:

1. **Ask an Agent** — describe an analysis, writing, or reasoning task; choose an existing enabled Agent and workspace.
2. **Change code** — describe a code change; choose a model and repository path, with an optional test command.
3. **Run a Workflow** — choose a saved Workflow and provide input JSON (default `{}`).

Each form submits directly to the existing service that owns that execution:
- Agent: `agentcatalog.Service.Run`
- Coding: `execution.Manager.Submit` with the existing `coding.CreateRequest`
- Workflow: `workflow.Engine.StartQueued`

No generic task orchestration service is introduced. The launcher is a presentation and routing layer only.

## Implementation boundaries

- Keep Go `html/template` + HTMX + existing CSS/JS; no SPA or new frontend dependency.
- Add only Dashboard routes, view data, handlers, templates, and focused tests.
- Reuse existing validation, workspace safety checks, idempotency, execution ownership, and runtime behavior.
- Do not add migrations, tables, queues, runtime services, or new execution states.
- Do not weaken same-origin checks; all POST actions require `sameOrigin`.
- Never expose provider credentials or secrets in rendered forms or error messages.
- Redirect successful submissions to the existing durable execution detail where available:
  - Agent: Execution detail when its execution record can be resolved; otherwise Agent page with a truthful message.
  - Coding: Coding task detail.
  - Workflow: Workflow Run detail.

## Acceptance criteria

1. Overview shows one clear primary **Start a task** action.
2. `/dashboard/new-task` explains the three task types in user language and presents only valid configured choices.
3. Agent submission requires an enabled Agent, an existing workspace, and a non-empty prompt; it ensures the workspace directory before running.
4. Coding submission reuses `coding.CreateRequest.Validate` and existing manager submission behavior; required repository/model/prompt validation remains authoritative.
5. Workflow submission requires a saved workflow and valid JSON object input, then redirects to the durable Run detail.
6. Missing prerequisites produce actionable guidance and links to the relevant setup page; no fake successful state is shown.
7. GET and POST handlers are covered by focused tests, including same-origin rejection and unavailable dependencies.
8. Existing P5/P6 runtime behavior and all current tests remain unchanged.

## Non-goals

- Automatically infer task type from free text.
- Create or edit Agent, Provider, Model, Workspace, or Workflow definitions inside the launcher.
- Add task history or a new persistence model.
- Change Runtime, scheduler, worker, checkpoint, Coding sandbox, or Workflow semantics.
- Merge P7 or this branch automatically.
