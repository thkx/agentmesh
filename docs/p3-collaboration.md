# P3 Collaboration Core

P3 adds durable collaboration primitives without replacing P2 Execution Manager.

- Workspace is a persisted collaboration and policy boundary, separate from filesystem workspaces.
- Agent Identity is a durable participant. Agent Definition remains the versioned execution recipe; runtime instances and sessions remain separate lifecycles.
- Channel stores membership and references the existing Conversation as message history. Threads are channel discussion branches.
- Collaboration Task is a general objective, distinct from Agent Runtime input Task and P2 Execution.
- Assignment claims use conditional SQL updates, expiring leases and monotonically increasing claim versions.
- Domain changes produce durable Outbox records. The Dispatcher delivers only to recipients defined by explicit event routing rules; it never invokes an LLM.

## Event routing

- task.assigned -> the assigned, non-disabled Agent Identity in the same Workspace.
- message.created -> non-disabled members of the referenced, non-archived Channel in the same Workspace.
- approval.requested -> the requesting Agent Identity.
- approval.approved / approval.rejected -> the approval requester's Agent Identity.
- Unknown event types are never workspace-broadcast; they are marked unroutable in Outbox diagnostics.

## Inbox lease protocol

Inbox consumption is at-least-once and requires a lease:
1. POST /inbox/:agent_id/claim with owner and lease_seconds.
2. Process the event idempotently.
3. Renew with POST /inbox/:agent_id/:event_id/renew if needed.
4. ACK using POST /inbox/:agent_id/:event_id/ack-lease with owner, lease_token and status (handled or ignored).
5. On transient failure, POST /inbox/:agent_id/:event_id/fail. The server applies bounded exponential backoff; after the retry ceiling, the event is terminally failed.

Lease owner and random lease token are both checked on renew, ACK and retry. An expired or superseded consumer cannot complete an event. Claim increments attempts and reclaims expired leases. The default retry ceiling is eight attempts. Existing legacy Inbox listing and ACK routes remain for compatibility; new consumers must use lease routes.

## Governance

The Decision Gate applies Workspace autonomy mode before privileged actions. Every evaluation is persisted in decision audit. Approval decisions are durable, expire after 24 hours, and emit Outbox events. Approval resolution does not itself execute the approved action; an execution consumer must consume the approval event and re-check current policy.

## Verification

Run `bash scripts/verify-p3.sh` for read-only formatting checks, build, vet and race tests. The script never rewrites source files.
