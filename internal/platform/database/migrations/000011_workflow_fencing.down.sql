DROP INDEX IF EXISTS idx_workflow_nodes_owner_fence;
DROP INDEX IF EXISTS idx_workflow_jobs_owner_fence;
ALTER TABLE workflow_node_executions DROP COLUMN fencing_token;
ALTER TABLE workflow_jobs DROP COLUMN fencing_token;
