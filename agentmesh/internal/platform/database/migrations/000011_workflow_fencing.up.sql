-- Monotonic fencing token: every successful claim advances the generation.
ALTER TABLE workflow_jobs ADD COLUMN fencing_token INTEGER NOT NULL DEFAULT 0;
ALTER TABLE workflow_node_executions ADD COLUMN fencing_token INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_workflow_jobs_owner_fence ON workflow_jobs(run_id, lease_owner, fencing_token);
CREATE INDEX idx_workflow_nodes_owner_fence ON workflow_node_executions(run_id, owner, fencing_token);
