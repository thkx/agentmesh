# P8 M66 — Unified Task Entry

**Branch:** `p8/m66-unified-task-entry`
**Base:** `p7/product-onboarding`
**Product outcome:** a first-time user can start a useful Agent task from one obvious entry point without first understanding the AgentMesh navigation.

## User experience

The Overview page exposes one primary action: **Start a task**. It opens `/dashboard/new-task`, a focused composer with:
- one enabled Agent selector,
- one existing Workspace selector,
- one plain-language task prompt.

The user submits the task directly. On success, AgentMesh redirects to the durable Execution detail when it can resolve the execution record; otherwise it redirects to the Agent page with a truthful status message.

The page also presents two clearly described advanced paths:
- **Change code** → existing Coding page.
- **Run a repeatable Workflow** → existing Workflow page.

These remain specialized flows because Coding needs repository and test context, while Workflow requires a saved and applied definition. M66 will not hide those real prerequisites behind a misleading generic form.

## Implementation boundaries

- Keep Go `html/template` + HTMX + existing CSS/JS; no SPA or new frontend dependency.
- Add only Dashboard routes, view data, handler, template, CSS, and focused tests.
- Reuse `agentcatalog.Service.Run`, existing workspace safety checks, and Execution lookup.
- Do not add migrations, tables, queues, runtime services, or new execution states.
- Do not weaken same-origin checks; task submission requires `sameOrigin`.
- Only enabled Agent definitions and existing workspaces are offered.
- Never expose provider credentials or secrets in rendered forms or error messages.

## Acceptance criteria

1. Overview shows one clear primary **Start a task** action.
2. `/dashboard/new-task` explains the minimum prerequisites in plain language and lists only enabled Agents and existing workspaces.
3. Agent submission requires an enabled Agent, an existing workspace, and a non-empty prompt.
4. Submission ensures the workspace directory exists before calling the Agent service.
5. Successful submissions redirect to the corresponding durable Execution detail when available.
6. Missing prerequisites produce actionable guidance and links to the relevant setup pages; no fake successful state is shown.
7. POST rejects cross-origin requests and unavailable dependencies safely.
8. Focused tests cover launcher rendering, empty prerequisites, and submission guards.
9. Existing P5/P6 runtime behavior and all current tests remain unchanged.

## Non-goals

- Automatically infer task type from free text.
- Replace or unify the Agent, Coding, or Workflow runtimes.
- Create or edit Provider, Model, Agent, Workspace, or Workflow definitions inside the launcher.
- Add task history or a new persistence model.
- Change Runtime, scheduler, worker, checkpoint, Coding sandbox, or Workflow semantics.
- Merge P7 or this branch automatically.
