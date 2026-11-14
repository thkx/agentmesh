# P3 Collaboration Core

P3 adds durable collaboration primitives without replacing P2 Execution Manager.

- Workspace is a persisted collaboration and policy boundary, separate from filesystem workspaces.
- Agent Identity is a durable participant. Agent Definition remains the versioned execution recipe; runtime instances and sessions remain separate lifecycles.
- Channel stores membership and references the existing Conversation as message history. Threads are channel discussion branches.
- Collaboration Task is a general objective, distinct from Agent Runtime input Task and P2 Execution.
- Assignment claims use conditional SQL updates, expiring leases and monotonically increasing claim versions.
- Domain changes produce durable Outbox records. The Dispatcher fans events into Agent Inboxes with (agent_id,event_id) idempotency and never invokes an LLM.

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

## Delivery and governance

Outbox dispatch is transactional: inbox fan-out and marking the outbox event published happen in one SQLite transaction. Duplicate inbox delivery is prevented by a unique (agent_id,event_id) constraint. The dispatcher does not invoke an LLM. An inbox event is not itself permission to wake an Agent; the consumer reads it, evaluates a decision, and acknowledges the event.

The Decision Gate applies Workspace autonomy mode before privileged actions. Directed mode requires approval for claim/delegate/execute actions; Hybrid requires approval for delegation and execution; Autonomous permits delegation and workflow execution but still requires approval for direct execution. Every evaluation is persisted in decision audit. Approval decisions are durable, expire after 24 hours, and emit Outbox events. Approval resolution does not itself execute the approved action; an execution consumer must consume the approval event and re-check current policy.

Agent-initiated handoff requires a requester identity and passes through the Decision Gate. Directed and Hybrid modes create or reuse a pending human approval; the handoff is materialized only after approval. Autonomous mode permits delegation within the Workspace's configured depth limit. A rejected or expired approval does not create a child task.

## Migrations and verification

- 000024_collaboration_core: workspace, identity, channel, thread, task, assignment, inbox and outbox.
- 000025_collaboration_governance: decision audit and human approvals.
- Run bash scripts/verify-p3.sh for formatting, build, vet and race tests.
