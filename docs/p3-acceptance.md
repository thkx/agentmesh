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

Every durable task result is passed through a pluggable `TaskResultEvaluator` and its assessment is persisted idempotently. The built-in conservative evaluator records `human_review`; it does not infer acceptance from a successful Runtime exit or unstructured output. Deployments may inject a policy/model-backed evaluator, whose output is constrained to `accepted`, `rejected`, or `human_review` and retained with its reason, confidence and evidence references. This is an assessment record, not an automatic parent-task completion: parent aggregation and explicit human resolution remain separate controls.
