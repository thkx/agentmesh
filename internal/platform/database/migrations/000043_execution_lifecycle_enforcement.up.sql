-- M45: enforce the common Execution/Attempt lifecycle at the persistence boundary.
-- M46: persist cancellation intent separately from the eventual runtime outcome.

CREATE TRIGGER executions_lifecycle_validate_update
BEFORE UPDATE OF status ON executions
WHEN OLD.status <> NEW.status
BEGIN
    SELECT CASE
        WHEN OLD.status IN ('succeeded','failed','canceled','timed_out')
        THEN RAISE(ABORT, 'terminal execution status is immutable')
        WHEN OLD.status = 'queued'
             AND NEW.status NOT IN ('running','succeeded','failed','canceled','timed_out','interrupted','needs_reconciliation')
        THEN RAISE(ABORT, 'invalid queued execution transition')
        WHEN OLD.status = 'running'
             AND NEW.status NOT IN ('succeeded','failed','canceled','timed_out','interrupted','needs_reconciliation')
        THEN RAISE(ABORT, 'invalid running execution transition')
        WHEN OLD.status IN ('interrupted','needs_reconciliation')
             AND NEW.status NOT IN ('succeeded','failed','canceled')
        THEN RAISE(ABORT, 'uncertain execution requires explicit resolution')
    END;
END;

-- Execution identity and accepted input are immutable after creation.
CREATE TRIGGER executions_identity_immutable
BEFORE UPDATE OF id,kind,runtime,task_id,input_json,idempotency_key,created_at ON executions
BEGIN
    SELECT RAISE(ABORT, 'execution identity and input are immutable');
END;

-- An Attempt can be completed exactly once. Its identity/input/start facts
-- cannot be rewritten, and terminal/unknown outcomes cannot be reopened.
CREATE TRIGGER execution_attempts_lifecycle_validate_update
BEFORE UPDATE ON execution_attempts
BEGIN
    SELECT CASE
        WHEN OLD.status <> 'running'
        THEN RAISE(ABORT, 'terminal execution attempt is immutable')
        WHEN NEW.status NOT IN ('succeeded','failed','canceled','timed_out','unknown')
        THEN RAISE(ABORT, 'execution attempt must finish with a terminal outcome')
        WHEN NEW.id <> OLD.id
          OR NEW.execution_id <> OLD.execution_id
          OR NEW.attempt_number <> OLD.attempt_number
          OR NEW.runtime <> OLD.runtime
          OR COALESCE(NEW.input_json,'') <> COALESCE(OLD.input_json,'')
          OR NEW.started_at <> OLD.started_at
          OR NEW.created_at <> OLD.created_at
        THEN RAISE(ABORT, 'execution attempt identity and input are immutable')
        WHEN NEW.finished_at IS NULL
        THEN RAISE(ABORT, 'terminal execution attempt requires finished_at')
    END;
END;

CREATE TABLE execution_control_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    event_key TEXT NOT NULL UNIQUE,
    execution_id TEXT NOT NULL REFERENCES executions(id) ON DELETE RESTRICT,
    event_type TEXT NOT NULL CHECK(event_type IN ('execution.cancel_requested')),
    actor_type TEXT NOT NULL DEFAULT 'system',
    actor_id TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'execution_manager',
    payload_json TEXT NOT NULL CHECK(json_valid(payload_json)),
    occurred_at INTEGER NOT NULL
);

CREATE INDEX idx_execution_control_events_execution_sequence
    ON execution_control_events(execution_id, sequence);

CREATE TRIGGER execution_control_events_no_update
BEFORE UPDATE ON execution_control_events
BEGIN
    SELECT RAISE(ABORT, 'execution control events are append-only');
END;

CREATE TRIGGER execution_control_events_no_delete
BEFORE DELETE ON execution_control_events
BEGIN
    SELECT RAISE(ABORT, 'execution control events are append-only');
END;
