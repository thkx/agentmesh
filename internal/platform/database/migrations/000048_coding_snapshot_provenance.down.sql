DROP INDEX IF EXISTS idx_coding_snapshots_attempt;
DROP INDEX IF EXISTS idx_coding_snapshots_execution;
ALTER TABLE coding_task_snapshots DROP COLUMN content_sha256;
ALTER TABLE coding_task_snapshots DROP COLUMN attempt_id;
ALTER TABLE coding_task_snapshots DROP COLUMN execution_id;
