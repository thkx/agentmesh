CREATE TABLE workflow_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL,
    sequence INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    node_id TEXT,
    worker_id TEXT,
    occurred_at INTEGER NOT NULL,
    data_json TEXT NOT NULL DEFAULT '{}',
    FOREIGN KEY(run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE,
    UNIQUE(run_id, sequence)
);
CREATE INDEX idx_workflow_events_run_sequence ON workflow_events(run_id, sequence);
CREATE INDEX idx_workflow_events_type_time ON workflow_events(event_type, occurred_at);
