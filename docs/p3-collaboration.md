# P3 Collaboration Core

P3 adds durable collaboration primitives without replacing P2 Execution Manager.

## Collaboration model

- Workspace is a persisted collaboration and policy boundary, separate from filesystem workspaces.
- Agent Identity is durable and distinct from Agent Definition, Runtime Instance and Session.
- Channel references the existing Conversation for message history; threads are discussion branches.
- Collaboration Task is a general objective, distinct from Agent Runtime input Task and P2 Execution.
- Assignment claims use conditional SQL updates, expiring leases and monotonically increasing claim versions.
- Domain changes produce durable Outbox records. The Dispatcher uses explicit recipient rules and never workspace-broadcasts unknown events.

## Runtime binding and sessions

Each Agent Identity can be bound to an enabled, versioned Agent Definition and a managed filesystem Workspace. The binding stores an immutable definition snapshot and model ID. Runtime Workspace IDs are resolved through the existing workspace manager; arbitrary filesystem paths are never accepted.

A persistent Agent Session tracks the active execution and last completed execution/task. It is an operational continuity record, not a claim that conversational memory is automatically injected into future prompts.

## Wakeup and execution

The Wakeup Worker consumes Inbox items with fenced leases. It does not invoke an LLM for every incoming event:
- task.assigned -> evaluate the Workspace Decision Gate for claim; if allowed, claim the Assignment, construct a validated Agent Runtime task from the bound definition, and submit it through P2 Execution Manager.
- In directed mode, a claim requiring approval is held. approval.approved rechecks the current Decision Gate and resumes the referenced assignment only when the persisted approval is still approved.
- approval.rejected -> reject the assignment and block its task.
- Other event types are safely acknowledged as non-executable notifications.

Execution idempotency is keyed by Assignment ID. If a process crashes after submitting but before acknowledging Inbox, the replay reuses the same Execution instead of submitting duplicate work. The worker renews both Inbox and Assignment leases while observing the Execution. On success it completes the Assignment; on terminal failure it rejects the Assignment and records a task failure event.

## Inbox lease protocol

1. POST /inbox/:agent_id/claim with owner and lease_seconds.
2. Process idempotently.
3. Renew with POST /inbox/:agent_id/:event_id/renew.
4. ACK with POST /inbox/:agent_id/:event_id/ack-lease using owner, lease_token and status (handled or ignored).
5. Retry transient failures with POST /inbox/:agent_id/:event_id/fail. Bounded exponential backoff applies; retry exhaustion moves the event to failed.

Legacy Inbox list and ACK routes remain for compatibility. New consumers use lease-aware endpoints.

## API additions

- POST /api/v1/collaboration/agents/:id/runtime-binding
- GET /api/v1/collaboration/agents/:id/runtime-binding

The binding request contains definition_id and runtime_workspace_id. The runtime workspace must already exist under the configured workspace root.

## Verification

Run `bash scripts/verify-p3.sh` for read-only formatting checks, build, vet and race tests.


Workspace `max_agent_concurrency` is enforced inside the assignment-claim SQL statement. Expired leases do not consume capacity; active claimed assignments reserve one slot until completion, rejection or lease expiry. The limit is shared by all wakeup workers and API callers using the same SQLite database.


Runtime bindings execute from their immutable definition snapshot, including the pinned model ID. The live definition is checked for continued enablement before execution, but editing a definition does not silently mutate an existing binding.

Approval rejection is acknowledged only after the target assignment is rejected, or when it is already terminal. Transient claim failures are retried rather than silently discarded. Inbox retry exhaustion is covered by an explicit dead-letter test.


## Outbox retry and dead-letter behavior

The dispatcher processes due Outbox records independently. A malformed or temporarily unroutable event records its attempt and schedules bounded exponential backoff; it does not prevent later valid events from being delivered. After eight failed attempts the event is marked dead-lettered (`published_at` set with a `dead-letter:` error prefix) for operator inspection.


Pending approvals are expired by the Outbox dispatcher. Expiration is an atomic state transition that emits `approval.expired` and routes the notification only to the original requester; expired requests no longer block a fresh decision request.


An Agent Identity can hold only one live Assignment lease at a time. This protects its single active Session pointer when multiple application processes consume the same Inbox; workspace concurrency remains an independent upper bound across different agents.


Task handoff is committed as one SQLite transaction: child Task, Assignment, state transition and both Outbox events either all persist or all roll back. A required handoff idempotency key ensures retries return the same child and assignment instead of creating duplicate delegated work.


A partial unique index enforces one pending approval per Workspace, requester, resource and action. Concurrent decision requests reuse the persisted pending approval rather than creating multiple review requests.


Delegation approval is bound to the exact handoff idempotency key, parent Task and recipient. Approving one handoff cannot authorize a different goal or recipient under the same parent Task.


Dead-lettered Outbox records remain queryable through `GET /api/v1/collaboration/outbox/dead-letters`, including attempts and the terminal error. They are not mixed into the pending Outbox feed.


Operators can retry a repaired dead-letter record with `POST /api/v1/collaboration/outbox/dead-letters/:id/requeue`. Requeue resets its retry counter and due time; delivery remains idempotent because Inbox receipts are unique by Agent and Event ID.


- GET /api/v1/collaboration/agents/:id/session returns the Agent Identity's active persistent Session and its current/last Execution pointers.


## Long-term memory context

Before submitting an assigned task to the P2 Execution Manager, Wakeup retrieves active global and Agent-scoped memories using the task title and description as the retrieval query. Retrieved content is appended to the bound Agent Definition's system prompt under an explicit untrusted-reference boundary. Memory retrieval failures fail the Inbox item through the existing bounded retry/dead-letter path; they are not silently ignored. Memory candidates remain inactive until explicitly promoted. Conversation Channel/Thread history is not yet automatically merged into the persistent Session context.


## Cross-session context assembly

When an assigned Agent wakes, the collaboration service loads recent messages only from channels in which the Agent is an explicit member and whose Workspace matches the Agent identity. The bounded context is appended to the task prompt as untrusted reference material. It excludes system/developer messages and tool payloads, and is capped by channel count, messages per channel, and total characters. This provides continuity across separate execution Sessions without treating conversation history as executable instructions.
