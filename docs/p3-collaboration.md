# P3 Collaboration Core

P3 adds durable collaboration primitives without replacing P2 Execution Manager.

- Workspace is a persisted collaboration and policy boundary, separate from filesystem workspaces.
- Agent Identity is a durable participant. Agent Definition remains the versioned execution recipe; runtime instances and sessions remain separate lifecycles.
- Channel stores membership and references the existing Conversation as message history. Threads are channel discussion branches.
- Collaboration Task is a general objective, distinct from Agent Runtime input Task and P2 Execution.
- Assignment claims use conditional SQL updates, expiring leases and monotonically increasing claim versions.
- Domain changes produce durable Outbox records. The Dispatcher fans events into Agent Inboxes with `(agent_id,event_id)` idempotency and never invokes an LLM.

## API

Routes are under `/api/v1/collaboration`: workspaces, agent identities, channels/messages/threads, tasks/assignments, outbox polling and idempotent inbox receipt.

- ` + "`POST /tasks/:id/handoff`" + ` creates a child task with a structured handoff payload and assigns it to a recipient in the same workspace.
- ` + "`POST /assignments/:id/renew`" + ` renews a lease only when owner and fencing version still match.
- ` + "`GET /inbox/:agent_id`" + ` reads pending inbox events; ` + "`POST /inbox/:agent_id/:event_id/ack`" + ` acknowledges handling.

## Delivery semantics

Outbox dispatch is transactional: inbox fan-out and marking the outbox event published happen in one SQLite transaction. Duplicate inbox delivery is prevented by a unique `(agent_id,event_id)` constraint. Agent wakeup/decision policy is intentionally not wired to the dispatcher yet; an inbox event is not itself permission to invoke a model.

## Migration

`000024_collaboration_core` creates the collaboration tables and does not alter P2 execution tables.

## Autonomy governance

The Decision Gate applies the Workspace autonomy mode before any privileged action. Directed mode requires approval for claim/delegate/execute actions; Hybrid requires approval for delegation and execution; Autonomous permits delegation and workflow execution but still requires approval for direct execution. Every evaluation is persisted in decision audit. Approval decisions are durable, expire after 24 hours, and emit Outbox events. Approval resolution does not itself execute the approved action; a later execution consumer must consume the approval event and re-check the current policy.

Agent-initiated handoff requires a requester identity and passes through the workspace Decision Gate. Directed and Hybrid modes create/reuse a pending human approval; the handoff is only materialized after that approval is granted. Autonomous mode permits delegation within the workspace's configured depth limit. A rejected or expired approval does not create a child task.
