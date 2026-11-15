-- A single append-only lifecycle ledger for the common Execution/Attempt model.
-- Runtime-specific event tables remain authoritative for their internal mechanics.
CREATE TABLE execution_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    event_key TEXT NOT NULL UNIQUE,
    execution_id TEXT NOT NULL REFERENCES executions(id) ON DELETE RESTRICT,
    attempt_id TEXT REFERENCES execution_attempts(id) ON DELETE RESTRICT,
    event_type TEXT NOT NULL CHECK(event_type IN (
        'execution.created',
        'execution.status_changed',
        'execution.runtime_ref_bound',
        'attempt.created',
        'attempt.status_changed'
    )),
    actor_type TEXT NOT NULL DEFAULT 'system',
    actor_id TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'execution_store',
    payload_json TEXT NOT NULL CHECK(json_valid(payload_json)),
    occurred_at INTEGER NOT NULL
);

CREATE INDEX idx_execution_events_execution_sequence
    ON execution_events(execution_id, sequence);
CREATE INDEX idx_execution_events_attempt_sequence
    ON execution_events(attempt_id, sequence)
    WHERE attempt_id IS NOT NULL;
CREATE INDEX idx_execution_events_type_occurred
    ON execution_events(event_type, occurred_at DESC);

-- The triggers make event capture atomic with the state write. The unique
-- event_key protects against duplicate trigger delivery/replayed migrations.
CREATE TRIGGER executions_event_after_insert
AFTER INSERT ON executions
BEGIN
    INSERT INTO execution_events (
        event_id, event_key, execution_id, event_type, payload_json, occurred_at
    ) VALUES (
        'evt-execution-created-' || NEW.id,
        'execution.created:' || NEW.id,
        NEW.id,
        'execution.created',
        json_object(
            'kind', NEW.kind,
            'runtime', NEW.runtime,
            'task_id', NEW.task_id,
            'status', NEW.status
        ),
        NEW.created_at
    );
END;

CREATE TRIGGER executions_event_after_status_update
AFTER UPDATE OF status ON executions
WHEN OLD.status <> NEW.status
BEGIN
    INSERT INTO execution_events (
        event_id, event_key, execution_id, event_type, payload_json, occurred_at
    ) VALUES (
        'evt-execution-status-' || NEW.id || '-' || NEW.updated_at,
        'execution.status_changed:' || NEW.id || ':' || NEW.updated_at,
        NEW.id,
        'execution.status_changed',
        json_object(
            'from', OLD.status,
            'to', NEW.status,
            'error', NEW.error_text
        ),
        NEW.updated_at
    );
END;

CREATE TRIGGER executions_event_after_runtime_ref_update
AFTER UPDATE OF runtime_ref ON executions
WHEN COALESCE(OLD.runtime_ref, '') <> COALESCE(NEW.runtime_ref, '')
BEGIN
    INSERT INTO execution_events (
        event_id, event_key, execution_id, event_type, payload_json, occurred_at
    ) VALUES (
        'evt-execution-runtime-ref-' || NEW.id || '-' || NEW.updated_at,
        'execution.runtime_ref_bound:' || NEW.id || ':' || NEW.updated_at,
        NEW.id,
        'execution.runtime_ref_bound',
        json_object('runtime_ref', NEW.runtime_ref),
        NEW.updated_at
    );
END;

CREATE TRIGGER execution_attempts_event_after_insert
AFTER INSERT ON execution_attempts
BEGIN
    INSERT INTO execution_events (
        event_id, event_key, execution_id, attempt_id, event_type, payload_json, occurred_at
    ) VALUES (
        'evt-attempt-created-' || NEW.id,
        'attempt.created:' || NEW.id,
        NEW.execution_id,
        NEW.id,
        'attempt.created',
        json_object(
            'attempt_number', NEW.attempt_number,
            'runtime', NEW.runtime,
            'runtime_ref', NEW.runtime_ref,
            'status', NEW.status
        ),
        NEW.created_at
    );
END;

CREATE TRIGGER execution_attempts_event_after_status_update
AFTER UPDATE OF status ON execution_attempts
WHEN OLD.status <> NEW.status
BEGIN
    INSERT INTO execution_events (
        event_id, event_key, execution_id, attempt_id, event_type, payload_json, occurred_at
    ) VALUES (
        'evt-attempt-status-' || NEW.id || '-' || NEW.updated_at,
        'attempt.status_changed:' || NEW.id || ':' || NEW.updated_at,
        NEW.execution_id,
        NEW.id,
        'attempt.status_changed',
        json_object(
            'attempt_number', NEW.attempt_number,
            'from', OLD.status,
            'to', NEW.status,
            'error', NEW.error_text
        ),
        NEW.updated_at
    );
END;

CREATE TRIGGER execution_events_no_update
BEFORE UPDATE ON execution_events
BEGIN
    SELECT RAISE(ABORT, 'execution events are append-only');
END;

CREATE TRIGGER execution_events_no_delete
BEFORE DELETE ON execution_events
BEGIN
    SELECT RAISE(ABORT, 'execution events are append-only');
END;
