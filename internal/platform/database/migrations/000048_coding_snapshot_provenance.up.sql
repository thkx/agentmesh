ALTER TABLE coding_task_snapshots ADD COLUMN execution_id TEXT NOT NULL DEFAULT '';
ALTER TABLE coding_task_snapshots ADD COLUMN attempt_id TEXT NOT NULL DEFAULT '';
ALTER TABLE coding_task_snapshots ADD COLUMN content_sha256 TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_coding_snapshots_execution ON coding_task_snapshots(execution_id) WHERE execution_id <> '';
CREATE INDEX idx_coding_snapshots_attempt ON coding_task_snapshots(attempt_id) WHERE attempt_id <> '';
