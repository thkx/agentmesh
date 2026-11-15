CREATE TABLE workflow_jobs (
    id TEXT PRIMARY KEY NOT NULL,
    run_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','interrupted')),
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    available_at INTEGER NOT NULL,
    lease_owner TEXT,
    lease_until INTEGER,
    last_error TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY(run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE
);
CREATE INDEX idx_workflow_jobs_claim ON workflow_jobs(status, available_at, created_at);
CREATE INDEX idx_workflow_jobs_lease ON workflow_jobs(status, lease_until);

CREATE TABLE workflow_node_executions (
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed','interrupted')),
    owner TEXT,
    lease_until INTEGER,
    output_json TEXT,
    error_text TEXT,
    attempt INTEGER NOT NULL DEFAULT 1,
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    PRIMARY KEY(run_id,node_id),
    FOREIGN KEY(run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE
);
CREATE INDEX idx_workflow_node_executions_status ON workflow_node_executions(status, lease_until);
