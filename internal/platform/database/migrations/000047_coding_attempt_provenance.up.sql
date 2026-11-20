ALTER TABLE coding_tasks ADD COLUMN attempt_id TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_coding_tasks_attempt ON coding_tasks(attempt_id) WHERE attempt_id <> '';
