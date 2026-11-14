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

## Delivery semantics

Outbox dispatch is transactional: inbox fan-out and marking the outbox event published happen in one SQLite transaction. Duplicate inbox delivery is prevented by a unique `(agent_id,event_id)` constraint. Agent wakeup/decision policy is intentionally not wired to the dispatcher yet; an inbox event is not itself permission to invoke a model.

## Migration

`000024_collaboration_core` creates the collaboration tables and does not alter P2 execution tables.
