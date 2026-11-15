# P3 Collaboration Core

P3 adds durable collaboration primitives without replacing P2 Execution Manager.

## Collaboration model

- Workspace is a persisted collaboration and policy boundary, separate from filesystem workspaces.
- Agent Identity is durable and distinct from Agent Definition, Runtime Instance and Session.
- Channel references the existing Conversation for message history; threads are discussion branches.
- Collaboration Task is a general objective, distinct from Agent Runtime input Task and P2 Execution.
- Assignment claims use conditional SQL updates, expiring leases and monotonically increasing claim versions.
- Tasks can declare normalized `required_capabilities`; assignment and Handoff reject Agents missing any requirement, and Wakeup rechecks the contract before starting the Runtime.
- Domain changes produce durable Outbox records. The Dispatcher uses explicit recipient rules and never workspace-broadcasts unknown events.

## Runtime binding and sessions

Each Agent Identity can be bound to an enabled, versioned Agent Definition and a managed filesystem Workspace. The binding stores an immutable definition snapshot and model ID. Runtime Workspace IDs are resolved through the existing workspace manager; arbitrary filesystem paths are never accepted.

A persistent Agent Session tracks the active execution and last completed execution/task. It is an operational continuity record, not a full transcript. Wakeup separately injects a bounded recent-message snapshot from channels where the Agent is an explicit member, alongside promoted memories, as untrusted reference context.

## Wakeup and execution

The Wakeup Worker consumes Inbox items with fenced leases. It does not invoke an LLM for every incoming event:
- task.assigned -> evaluate the Workspace Decision Gate for claim; if allowed, claim the Assignment, construct a validated Agent Runtime task from the bound definition, and submit it through P2 Execution Manager.
- In directed mode, a claim requiring approval is held. approval.approved rechecks the current Decision Gate and resumes the referenced assignment only when the persisted approval is still approved.
- approval.rejected -> reject the assignment and block its task.
- task.completed -> persist the result receipt and evaluate it with the receiving Agent's pinned model in Hybrid/Autonomous mode; Directed mode requires human review without invoking the model. The evaluator must cite supplied evidence IDs, and acceptance requires a passed deterministic Artifact verification.
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


## Capability-constrained work routing

Agent Identity capabilities are normalized lowercase identifiers (letters, digits, dot, underscore and hyphen). A Task may require up to 32 capabilities; an Agent may advertise up to 128. Every required capability must be present on the selected Agent. The same check is applied to Handoff recipients before a delegation approval is requested. The Wakeup worker repeats the check after claiming an Assignment so stale or manually altered state fails closed before Runtime execution.

## Result and Artifact lifecycle

A successful Runtime result is stored in the durable Agent result ledger. The result evaluator receives the Task, raw result and a bounded snapshot of linked Artifact versions, evidence and deterministic verification history. Model outcomes are constrained to `accepted`, `rejected` and `human_review`; model confidence alone is not evidence. Directed Workspaces always require human review. In Hybrid/Autonomous Workspaces, acceptance requires confidence >= 0.85, valid evidence references and at least one passed deterministic Artifact verification.

A rejected result or a rejected submitted Artifact blocks its source Task. The existing assignment endpoint can then assign that blocked Task to another eligible Agent. Previous Assignment results, Artifact versions and review decisions remain immutable. Replaying the same rejection does not re-block a Task after it has been reassigned.

Artifact review and verification are separate: a deterministic verification pass does not constitute human acceptance. Each Artifact Version is immutable, evidence is tied to an exact version, submission pins a version, and a subsequent version must be verified and submitted again.

## Additional collaboration API

- `GET /api/v1/collaboration/agents/:id/task-results`
- `GET /api/v1/collaboration/agents/:id/task-results/:assignment_id`
- `POST /api/v1/collaboration/agents/:id/task-results/:assignment_id/review`
- `GET /api/v1/collaboration/tasks/:id/progress`
- `POST /api/v1/collaboration/assignments/:id/fail`
- `POST /api/v1/collaboration/assignments/:id/retry`
- `GET /api/v1/collaboration/agents/:id/decision-audit`
- `POST /api/v1/collaboration/artifacts`
- `GET /api/v1/collaboration/workspaces/:id/artifacts`
- `GET /api/v1/collaboration/artifacts/:id/versions/:version`
- `GET|POST /api/v1/collaboration/artifacts/:id/evidence`
- `POST /api/v1/collaboration/artifacts/:id/verify`
- `GET /api/v1/collaboration/artifacts/:id/verifications`
- `GET /api/v1/collaboration/artifacts/:id/reviews`
- `POST /api/v1/collaboration/artifacts/:id/submit`
- `POST /api/v1/collaboration/artifacts/:id/review`

For the complete acceptance matrix, verification procedure and intentionally deferred P3 boundaries, see [P3 Acceptance Record](p3-acceptance.md).


## Durable Agent Session transcript

Managed collaboration executions append immutable `execution_input`, `execution_result`, and `execution_failure` records to the Agent's active Session. A uniqueness key over Session, Execution and entry type makes retries idempotent; conflicting replays are rejected. Entry content is bounded to 256 KiB, SHA-256 fingerprinted, and marked when truncated. Append-only database triggers protect the history.

Before a later assignment is submitted, the Wakeup worker injects a bounded chronological snapshot of prior Session entries as untrusted historical context. The complete record is available at `GET /api/v1/collaboration/agents/:id/session/transcript`. Channel messages remain separately persisted in Conversation storage and are injected through the explicit channel-membership snapshot; the Session transcript is an execution continuity record, not a replacement for the full Conversation archive.
