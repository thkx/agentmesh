ALTER TABLE coding_tasks ADD COLUMN execution_id TEXT NOT NULL DEFAULT '';
ALTER TABLE coding_tasks ADD COLUMN outcome_unknown INTEGER NOT NULL DEFAULT 0 CHECK(outcome_unknown IN (0,1));
CREATE INDEX idx_coding_tasks_execution ON coding_tasks(execution_id) WHERE execution_id <> '';
CREATE TRIGGER coding_task_events_no_update BEFORE UPDATE ON coding_task_events BEGIN SELECT RAISE(ABORT, 'coding task events are append-only'); END;
CREATE TRIGGER coding_task_events_no_delete BEFORE DELETE ON coding_task_events BEGIN SELECT RAISE(ABORT, 'coding task events are append-only'); END;
CREATE TRIGGER coding_task_snapshots_no_update BEFORE UPDATE ON coding_task_snapshots BEGIN SELECT RAISE(ABORT, 'coding task snapshots are immutable'); END;
