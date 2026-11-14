DROP INDEX IF EXISTS idx_collaboration_inbox_claim;
ALTER TABLE collaboration_inbox DROP COLUMN last_error;
ALTER TABLE collaboration_inbox DROP COLUMN next_attempt_at;
ALTER TABLE collaboration_inbox DROP COLUMN attempts;
ALTER TABLE collaboration_inbox DROP COLUMN lease_expires_at;
ALTER TABLE collaboration_inbox DROP COLUMN lease_token;
ALTER TABLE collaboration_inbox DROP COLUMN lease_owner;
