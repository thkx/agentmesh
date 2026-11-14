# P3 Collaboration Core

P3 adds durable collaboration primitives without replacing P2 Execution Manager.

- Workspace is a persisted collaboration and policy boundary, separate from filesystem workspaces.
- Agent Identity is a durable participant. Agent Definition remains the versioned execution recipe; runtime instances and sessions are distinct lifecycles.
- Channel stores membership and references the existing Conversation as its message history. Thread is a channel discussion branch.
- Collaboration Task is a general objective, distinct from the Agent Runtime input Task and P2 Execution.
- Assignment uses conditional SQL claim updates, expiring leases and monotonically increasing claim versions.
- Outbox events are persisted alongside primary workspace/task/assignment mutations where supported. Inbox receipt is idempotent by (agent_id,event_id).

Routes are under `/api/v1/collaboration`: workspaces, agents, channels/messages/threads, tasks/assignments, outbox and inbox. The outbox is exposed for polling; background dispatch and policy-based Agent wakeup are not yet enabled. This is intentional: no incoming message automatically invokes an LLM until the wakeup policy and dispatcher are implemented and verified.

Migration: `000024_collaboration_core`.
