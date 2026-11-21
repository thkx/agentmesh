CREATE TRIGGER coding_task_steps_no_update
BEFORE UPDATE ON coding_task_steps
BEGIN
  SELECT RAISE(ABORT, 'coding task steps are immutable');
END;

CREATE TRIGGER coding_task_steps_no_delete
BEFORE DELETE ON coding_task_steps
BEGIN
  SELECT RAISE(ABORT, 'coding task steps are immutable');
END;
