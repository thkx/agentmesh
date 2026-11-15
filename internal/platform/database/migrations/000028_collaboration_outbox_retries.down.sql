DROP INDEX IF EXISTS idx_collaboration_outbox_due;
ALTER TABLE collaboration_outbox DROP COLUMN next_attempt_at;
