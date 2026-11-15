# P3 Acceptance Record

P3 is the collaboration and governed execution foundation layered on top of the P2 Execution Manager. It is not a claim that AgentMesh already performs autonomous multi-agent teamwork for every message type.

## Implemented acceptance criteria

| Area | Acceptance |
|---|---|
| Collaboration identity | Persisted Workspace and Agent Identity, separate from Agent Definition, filesystem Workspace and Runtime Session |
| Conversation | Channels and threads reuse the existing Conversation message store |
| Task model | General collaboration Tasks, Assignments, delegation lineage and idempotency |
| Assignment safety | Atomic claim, lease expiry, fencing version, renewal and terminal transitions |
| Agent serialization | One active Assignment lease per Agent Identity |
| Workspace governance | `max_agent_concurrency` enforced in the claim statement across all agents in a Workspace |
| Handoff | Child Task, Assignment and Outbox events commit or roll back together; retries return the same child and assignment |
| Approval | Directed/Hybrid/Autonomous gate, durable audit, pending-request dedupe, exact-request-scoped delegation approval and expiry notification |
| Outbox | Transactional event delivery, explicit recipient allow-list, bounded retry, dead-letter inspection and operator requeue |
| Inbox | Durable receipt, lease owner/token fencing, renewal, ACK, exponential retry and terminal failure |
| Runtime binding | Agent Identity pinned to an enabled Definition version/model snapshot and managed filesystem Workspace |
| Session | Persistent active Session and execution history pointer |
| Wakeup | Assigned Task and approved/rejected Assignment events feed the P2 Execution Manager through an idempotency key derived from Assignment ID; promoted global and Agent-scoped memories are injected as untrusted reference context |
| Recovery | Expired Inbox leases are reclaimable; duplicate wakeups reuse the same Execution; expired Assignment claims can be reclaimed using fencing |
| Verification | Build, Vet, full Race suite, migration-backed service tests and a Wakeup-to-Execution integration test |

## Verification

Run on the project development environment:

```bash
bash scripts/verify-p3.sh
```

The script performs a read-only `gofmt` check, `go build ./...`, `go vet ./...` and `go test -race ./...`.

GitHub Actions runs the same build, vet and race verification on every push to `p3/collaboration`. A green run confirms repository-level CI; it does not replace a local deployment smoke test using the user's configured providers and real runtime adapters.

## Operational recovery

- Inbox: inspect `GET /api/v1/collaboration/inbox/:agent_id?status=failed`; lease-aware consumers should use claim/renew/ack-lease/fail endpoints.
- Outbox: inspect `GET /api/v1/collaboration/outbox`.
- Outbox dead letters: inspect `GET /api/v1/collaboration/outbox/dead-letters`.
- After correcting the cause, retry one event with `POST /api/v1/collaboration/outbox/dead-letters/:id/requeue`.
- Assignment claims are fenced by owner and claim version. Do not manually overwrite lease fields while a worker is running.

## Explicit P3 boundaries

These are intentionally not represented as completed P3 behavior:

1. `message.created` is delivered only to channel members as a notification. It does not automatically invoke an LLM or generate a chat reply. Inbox delivery is not equivalent to an Agent decision.
2. Persistent Session records track execution continuity. Assignment wakeup retrieves promoted global and Agent-scoped memories, but does not yet assemble full Channel/Thread conversation history into each Agent Session.
3. The Wakeup integration test uses a deterministic fake Execution adapter. A real provider/model/runtime smoke test must be run with the deployment's actual credentials and configured adapter.
4. The collaboration API is delivered; a dedicated collaboration UI, multi-computer Runner fleet, artifact review UI and automatic agent-to-agent conversational loop are later work.

These boundaries preserve the P3 safety model: only explicitly supported event types cause execution, approval is rechecked before privileged work, and unknown notifications are never broadcast into arbitrary execution.


Inbox failed events can be inspected with `GET /api/v1/collaboration/inbox/:agent_id?status=failed` and requeued individually with `POST /api/v1/collaboration/inbox/:agent_id/:event_id/requeue` after correcting the underlying cause.


The Decision Gate currently exposes executable privileged actions only for Assignment claim and exact-request Handoff delegation. `execute_workflow` and free-form `direct_execute` are rejected until their own durable execution/approval adapters exist; this prevents creating approvals that no worker can safely consume.


### Cross-session collaboration context

Assigned Agent wakeups assemble a bounded snapshot of recent messages from channels where that Agent is an explicit member. The snapshot is limited to five channels, twelve recent messages per channel, and 12,000 characters by default. Only user and assistant messages are included; system/developer instructions and tool payloads are excluded. Conversation content is injected as untrusted task reference context. Channel membership and Workspace identity are checked in the query; messages from unrelated Workspace channels are never included. The wakeup integration test verifies that an Agent's channel history reaches the Runtime task prompt.


### Durable execution result handoff

A successful Agent execution now completes its Assignment and writes a `task.completed` Outbox event in the same SQLite transaction. The event includes the Task ID, Assignment ID, Execution ID, and the Runtime result payload. The Outbox is the durable boundary for subsequent result delivery/materialization; the Wakeup worker does not rely on an in-memory callback after execution. The integration test verifies that the execution result is present in the persisted completion event.


### Durable Agent result receipt

A task.completed Inbox event is consumed by the recipient Agent's Wakeup worker and materialized into agent_task_results. The ledger preserves the originating Workspace, Task, Assignment, Execution, recipient Agent and raw JSON result. A unique (assignment_id, recipient_agent_id) constraint makes delivery idempotent across worker crashes and retries. The recipient can retrieve its newest results through the collaboration service's ListTaskResults method. This is durable result receipt, not yet an LLM-driven follow-up decision or automatic parent-task completion.


### Result decision boundary (P3-2)

Every durable task result is passed through a pluggable `TaskResultEvaluator` and its assessment is persisted idempotently. A directly constructed Wakeup Worker defaults to conservative `human_review`; the application bootstrap wires an LLM-backed evaluator that uses the receiving Agent's pinned Runtime model. The model output is constrained to `accepted`, `rejected`, or `human_review`, requires confidence of at least 0.85 for an automatic assessment, and must cite exact evidence/verification IDs from the supplied context. Acceptance additionally requires at least one passed deterministic Artifact verification. Directed Workspaces always require human review; Hybrid and Autonomous may use evidence-grounded model assessment. This is not an automatic parent-task completion: parent aggregation and explicit human resolution remain separate controls.


### P3-2 governance and recovery

Human result review persists reviewer identity, decision note and review time, plus an append-only audit record. Parent progress is computed from child Task status and the parent Agent's own result assessment; other recipients' notifications cannot satisfy acceptance. A parent is ready for its own review only when every child has a durable result accepted by that parent Agent. Aggregation never auto-completes the parent. Assignment failures are fenced by lease owner and claim version, transition the Task to blocked, and emit an Outbox event. Retry is explicit, reuses the Assignment identity, increments a bounded retry counter, and emits its own event.


A failed Assignment with no durable result may be retried in place within its retry budget. Once a result exists (including a human-rejected result), the same Assignment cannot be reused because its result receipt is immutable; recovery must create a new Assignment for another eligible Agent. Blocked Tasks can therefore be reassigned, and parent progress evaluates the latest result delivered to the parent Agent for each child rather than treating historical rejected attempts as the current outcome.

### P3-3 Artifact, Evidence and Verification foundation

Artifacts are Workspace-scoped deliverables linked optionally to a source Task and creator Agent. The initial local-first storage implementation keeps bounded content in SQLite (maximum 2 MiB per version, aligned with the HTTP request limit); each version stores a SHA-256 digest and immutable metadata. New versions never overwrite prior versions. A later storage adapter may move bytes to a content-addressed filesystem/blob backend without changing the Artifact domain contract.

Evidence is attached to one exact Artifact Version and is append-only. Supported evidence kinds are `test`, `build`, `diff`, `source_reference`, `tool_log`, `metric`, and `manual`. A supplied content digest must match the referenced Artifact Version. Evidence statements and source references are claims/provenance, not proof by themselves.

Verification is deterministic and policy-driven: required evidence kinds are checked against records attached to the exact version, and the stored content digest is recomputed. Outcomes are `passed`, `needs_evidence`, or `failed`. A `passed` result means only that the configured deterministic policy passed; it does not mean a human accepted the deliverable or that an arbitrary claim in a source reference is true. Model review is not counted as objective evidence.

HTTP endpoints:
- `POST /api/v1/collaboration/artifacts`
- `GET /api/v1/collaboration/artifacts/:id`
- `GET /api/v1/collaboration/artifacts/:id/versions/:version`
- `POST /api/v1/collaboration/artifacts/:id/versions`
- `GET|POST /api/v1/collaboration/artifacts/:id/evidence`
- `POST /api/v1/collaboration/artifacts/:id/verify`

Run `bash scripts/verify-p3.sh` to validate formatting, build, vet, migrations and race tests. The first implementation deliberately does not claim external blob storage, artifact review UI, signature verification, or semantic truth validation.

### Artifact submission and human acceptance lifecycle

An Artifact remains `draft` until its current immutable version has a deterministic `passed` verification. Submission pins the exact version under review; a later version resets the Artifact to `draft` and requires a new verification and submission. Human review accepts or rejects only the pinned submitted version. One review decision is allowed per Artifact Version; an identical retry is idempotent, while a conflicting second decision is rejected.

Artifact review records and verification records are append-only at the SQLite trigger level. The current `reviewer_id` is supplied by the API caller, just like the P3-2 task-result review identity; production multi-user deployments must bind it to an authenticated principal in middleware rather than trusting arbitrary request JSON.

Additional endpoints:
- `POST /api/v1/collaboration/artifacts/:id/submit`
- `POST /api/v1/collaboration/artifacts/:id/review`

Artifact discovery and audit history are also available through `GET /api/v1/collaboration/workspaces/:id/artifacts` and `GET /api/v1/collaboration/artifacts/:id/verifications`. Listing endpoints return bounded metadata/history; version content is fetched separately by exact version number.


### P3-4 Artifact-aware Agent result evaluation

The result-evaluation seam now supports an optional contextual evaluator. In addition to the Task and durable Runtime result, it can receive a bounded snapshot of Artifacts explicitly linked to that Task, including the exact current immutable version, SHA-256 digest, evidence records and recent deterministic verification history.

The context is Workspace-scoped and bounded to eight Artifacts, 4 MiB of included Artifact content, 50 evidence records and five verification records per Artifact. When the content budget is exhausted, the evaluator still receives version metadata and digest with `content_included=false`; it must not infer that omitted content was inspected. Existing `TaskResultEvaluator` implementations remain compatible and continue receiving the original Task/Result pair. The conservative default still returns `human_review`; this feature does not silently auto-accept results.


Model-based result assessment also respects Workspace autonomy: `directed` always returns `human_review` without invoking the model; `hybrid` and `autonomous` may use the evidence-grounded evaluator. Missing or invalid autonomy policy fails closed to human review. This is an assessment of a child result, not permission to execute tools, delegate, or complete the parent Task.


### P3-5 Rejected result and reassignment lifecycle

A new, idempotently stored `rejected` Agent assessment moves its Task to `blocked` in the same SQLite transaction as the assessment and `task.result_rejected` Outbox event. The prior Assignment, Runtime result and assessment remain immutable history. The existing task-assignment API can then assign the blocked Task to a different Agent; the new Assignment and `task.assigned` event are committed atomically. Replaying the same assessment does not re-block a subsequently reassigned Task or emit duplicate events. A rejected result cannot be retried in place or overwritten.


Artifact rejection is also a rework boundary: rejecting a submitted Artifact linked to a source Task moves that Task to `blocked` and writes a `task.artifact_rejected` Outbox event in the same transaction as the immutable review record. The Task can then be assigned to another Agent without altering the rejected Artifact Version or its review history. An idempotent review replay does not block a later reassignment again.


Decision policy history is queryable per Agent Identity through `GET /api/v1/collaboration/agents/:id/decision-audit`. The endpoint returns bounded, newest-first audit metadata and does not expose approval secrets or mutable state.
