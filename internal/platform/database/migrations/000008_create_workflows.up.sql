CREATE TABLE workflows (
 id TEXT NOT NULL,
 version INTEGER NOT NULL,
 name TEXT NOT NULL,
 definition_json TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 PRIMARY KEY(id, version)
);
CREATE INDEX idx_workflows_latest ON workflows(id, version DESC);
CREATE TABLE workflow_runs (
 id TEXT PRIMARY KEY NOT NULL,
 workflow_id TEXT NOT NULL,
 workflow_version INTEGER NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('queued','running','waiting_approval','succeeded','failed','rejected','canceled')),
 current_node TEXT,
 state_json TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 FOREIGN KEY(workflow_id, workflow_version) REFERENCES workflows(id, version)
);
CREATE INDEX idx_workflow_runs_workflow ON workflow_runs(workflow_id, created_at DESC);
CREATE INDEX idx_workflow_runs_status ON workflow_runs(status, updated_at);
