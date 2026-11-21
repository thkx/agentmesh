CREATE TRIGGER coding_task_snapshots_no_delete
BEFORE DELETE ON coding_task_snapshots
BEGIN
  SELECT RAISE(ABORT, 'coding task snapshots are immutable');
END;
