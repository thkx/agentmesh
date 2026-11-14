CREATE TABLE executions (
    id TEXT PRIMARY KEY NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('agent','coding','workflow','cli')),
    runtime TEXT NOT NULL CHECK(runtime IN ('agent','coding','workflow','cli')),
    runtime_ref TEXT,
    task_id TEXT,
    status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','canceled','timed_out','interrupted','needs_reconciliation')),
    input_json TEXT CHECK(input_json IS NULL OR json_valid(input_json)),
    result_json TEXT CHECK(result_json IS NULL OR json_valid(result_json)),
    error_text TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT UNIQUE,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    started_at INTEGER,
    finished_at INTEGER
);
CREATE UNIQUE INDEX idx_executions_runtime_ref ON executions(runtime,runtime_ref) WHERE runtime_ref IS NOT NULL AND runtime_ref <> '';
CREATE INDEX idx_executions_status_created ON executions(status,created_at DESC);
CREATE INDEX idx_executions_kind_created ON executions(kind,created_at DESC);
CREATE INDEX idx_executions_task ON executions(task_id);

CREATE TABLE execution_attempts (
    id TEXT PRIMARY KEY NOT NULL,
    execution_id TEXT NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL CHECK(attempt_number > 0),
    runtime TEXT NOT NULL CHECK(runtime IN ('agent','coding','workflow','cli')),
    runtime_ref TEXT,
    status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed','canceled','timed_out','unknown')),
    input_json TEXT CHECK(input_json IS NULL OR json_valid(input_json)),
    result_json TEXT CHECK(result_json IS NULL OR json_valid(result_json)),
    error_text TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(execution_id,attempt_number)
);
CREATE INDEX idx_execution_attempts_execution ON execution_attempts(execution_id,attempt_number);
