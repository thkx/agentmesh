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
| Wakeup | Assigned Task and approved/rejected Assignment events feed the P2 Execution Manager through an idempotency key derived from Assignment ID |
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
2. Persistent Session records track execution continuity; they do not yet provide a complete cross-session memory/context assembly policy.
3. The Wakeup integration test uses a deterministic fake Execution adapter. A real provider/model/runtime smoke test must be run with the deployment's actual credentials and configured adapter.
4. The collaboration API is delivered; a dedicated collaboration UI, multi-computer Runner fleet, artifact review UI and automatic agent-to-agent conversational loop are later work.

These boundaries preserve the P3 safety model: only explicitly supported event types cause execution, approval is rechecked before privileged work, and unknown notifications are never broadcast into arbitrary execution.
