CREATE TABLE agent_definitions (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL,
    model_id TEXT NOT NULL,
    temperature REAL NOT NULL DEFAULT 0,
    max_steps INTEGER NOT NULL DEFAULT 12,
    max_tool_calls INTEGER NOT NULL DEFAULT 8,
    max_output_tokens INTEGER NOT NULL DEFAULT 0,
    timeout_seconds INTEGER NOT NULL DEFAULT 900,
    approved_tools_json TEXT NOT NULL DEFAULT '[]',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
    version INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY(model_id) REFERENCES models(id) ON DELETE RESTRICT
);
CREATE INDEX idx_agent_definitions_enabled ON agent_definitions(enabled,name);
CREATE TABLE agent_definition_versions (
    definition_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    snapshot_json TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY(definition_id,version),
    FOREIGN KEY(definition_id) REFERENCES agent_definitions(id) ON DELETE CASCADE
);
CREATE TABLE agent_runs (
    id TEXT PRIMARY KEY NOT NULL,
    definition_id TEXT NOT NULL,
    task_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed','canceled','timed_out')),
    result_json TEXT,
    error_text TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    finished_at INTEGER,
    FOREIGN KEY(definition_id) REFERENCES agent_definitions(id) ON DELETE RESTRICT
);
CREATE INDEX idx_agent_runs_definition ON agent_runs(definition_id,created_at DESC);
CREATE INDEX idx_agent_runs_status ON agent_runs(status,updated_at);
CREATE TABLE tool_execution_audit (
    id TEXT PRIMARY KEY NOT NULL,
    tool_name TEXT NOT NULL,
    workspace TEXT NOT NULL DEFAULT '',
    allowed INTEGER NOT NULL CHECK(allowed IN (0,1)),
    approved INTEGER NOT NULL CHECK(approved IN (0,1)),
    started_at INTEGER NOT NULL,
    duration_ns INTEGER NOT NULL DEFAULT 0,
    error_text TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_tool_execution_audit_started ON tool_execution_audit(started_at DESC);
