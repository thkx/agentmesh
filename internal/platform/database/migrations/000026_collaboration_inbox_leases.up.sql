-- Durable lease-based Inbox consumption. Existing pending/processing rows remain claimable.
ALTER TABLE collaboration_inbox ADD COLUMN lease_owner TEXT NOT NULL DEFAULT '';
ALTER TABLE collaboration_inbox ADD COLUMN lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE collaboration_inbox ADD COLUMN lease_expires_at INTEGER;
ALTER TABLE collaboration_inbox ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts >= 0);
ALTER TABLE collaboration_inbox ADD COLUMN next_attempt_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE collaboration_inbox ADD COLUMN last_error TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_collaboration_inbox_claim
ON collaboration_inbox(agent_id, status, next_attempt_at, lease_expires_at, received_at);
