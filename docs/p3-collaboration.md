# P3 Collaboration Core

P3 adds durable collaboration primitives without replacing P2 Execution Manager.

- Workspace is a persisted collaboration and policy boundary, separate from filesystem workspaces.
- Agent Identity is a durable participant. Agent Definition remains the versioned execution recipe; runtime instances and sessions remain separate lifecycles.
- Channel stores membership and references the existing Conversation as message history. Threads are channel discussion branches.
- Collaboration Task is a general objective, distinct from Agent Runtime input Task and P2 Execution.
- Assignment claims use conditional SQL updates, expiring leases and monotonically increasing claim versions.
- Domain changes produce durable Outbox records. The Dispatcher delivers only to recipients defined by explicit event routing rules; it never invokes an LLM.

## API

Routes are under /api/v1/collaboration:

- GET|POST /workspaces; GET /workspaces/:id
- GET /workspaces/:id/agents; POST /agents; GET /agents/:id
- GET /workspaces/:id/channels; POST /channels; GET /channels/:id
- GET|POST /channels/:id/messages; POST /channels/:id/threads
- GET /workspaces/:id/tasks; POST /tasks; GET /tasks/:id
- POST /tasks/:id/assignments; POST /tasks/:id/handoff
- POST /assignments/:id/claim; POST /assignments/:id/renew; POST /assignments/:id/complete
- POST /decisions/evaluate
- GET /workspaces/:id/approvals; POST /approvals/:id/resolve
- GET /outbox; GET /inbox/:agent_id; POST /inbox/:agent_id/events; POST /inbox/:agent_id/:event_id/ack

## Event routing and delivery

The dispatcher uses an allow-list:
- task.assigned -> the assigned, non-disabled Agent Identity in the same Workspace.
- message.created -> non-disabled members of the referenced, non-archived Channel in the same Workspace.
- approval.requested -> the requesting Agent Identity.
- approval.approved / approval.rejected -> the approval requester's Agent Identity.

Unknown event types are never workspace-broadcast. They are marked published with an `unroutable:` diagnostic in the Outbox `last_error` field so that unsupported events do not block later events. Malformed known-event payloads and invalid recipients are treated as dispatch errors and remain pending for operator investigation.

Fan-out and marking the Outbox event published happen in one SQLite transaction. Duplicate Inbox delivery is prevented by the unique (agent_id,event_id) constraint. The dispatcher does not invoke an LLM. Inbox consumption and execution are separate concerns: an Inbox event is not itself permission to wake an Agent; a consumer must evaluate a decision and acknowledge the event.

## Governance

The Decision Gate applies Workspace autonomy mode before privileged actions. Every evaluation is persisted in decision audit. Approval decisions are durable, expire after 24 hours, and emit Outbox events. Approval resolution does not itself execute the approved action; an execution consumer must consume the approval event and re-check current policy.

Agent-initiated handoff requires a requester identity and passes through the Decision Gate. A rejected or expired approval does not create a child task.

## Migrations and verification

- 000024_collaboration_core: workspace, identity, channel, thread, task, assignment, inbox and outbox.
- 000025_collaboration_governance: decision audit and human approvals.
- Run `bash scripts/verify-p3.sh` for read-only formatting checks, build, vet and race tests. The script never rewrites source files.
