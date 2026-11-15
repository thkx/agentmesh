CREATE TABLE agent_runtime_bindings (
    agent_id TEXT PRIMARY KEY NOT NULL REFERENCES agent_identities(id) ON DELETE CASCADE,
    definition_id TEXT NOT NULL REFERENCES agent_definitions(id) ON DELETE RESTRICT,
    definition_version INTEGER NOT NULL CHECK(definition_version > 0),
    runtime_workspace_id TEXT NOT NULL,
    definition_snapshot_json TEXT NOT NULL CHECK(json_valid(definition_snapshot_json)),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_agent_runtime_bindings_definition ON agent_runtime_bindings(definition_id, definition_version);

CREATE TABLE agent_sessions (
    id TEXT PRIMARY KEY NOT NULL,
    agent_id TEXT NOT NULL REFERENCES agent_identities(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','paused','closed')),
    current_execution_id TEXT,
    last_execution_id TEXT,
    last_task_id TEXT,
    execution_count INTEGER NOT NULL DEFAULT 0 CHECK(execution_count >= 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_agent_sessions_one_active
ON agent_sessions(agent_id) WHERE status='active';
CREATE INDEX idx_agent_sessions_agent_updated ON agent_sessions(agent_id, updated_at DESC);
