ALTER TABLE collaboration_outbox ADD COLUMN next_attempt_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_collaboration_outbox_due ON collaboration_outbox(published_at,next_attempt_at,created_at,id);
