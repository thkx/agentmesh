DROP TRIGGER IF EXISTS coding_task_snapshots_no_update;
DROP TRIGGER IF EXISTS coding_task_events_no_delete;
DROP TRIGGER IF EXISTS coding_task_events_no_update;
DROP INDEX IF EXISTS idx_coding_tasks_execution;
ALTER TABLE coding_tasks DROP COLUMN outcome_unknown;
ALTER TABLE coding_tasks DROP COLUMN execution_id;
