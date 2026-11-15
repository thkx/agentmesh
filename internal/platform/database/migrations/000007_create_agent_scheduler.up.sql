CREATE TABLE agent_jobs (
    id TEXT PRIMARY KEY NOT NULL,
    task_id TEXT NOT NULL UNIQUE,
    task_json TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued','running','succeeded','failed','interrupted','canceled')),
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    available_at INTEGER NOT NULL,
    lease_owner TEXT,
    lease_until INTEGER,
    execution_started INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_agent_jobs_claim ON agent_jobs(status, available_at, created_at);
CREATE INDEX idx_agent_jobs_lease ON agent_jobs(status, lease_until);

CREATE TABLE agent_tool_invocations (
    idempotency_key TEXT PRIMARY KEY NOT NULL,
    task_id TEXT NOT NULL,
    tool_call_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('started','completed','indeterminate')),
    result_json TEXT,
    error_text TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_agent_tool_invocations_task ON agent_tool_invocations(task_id, created_at);
