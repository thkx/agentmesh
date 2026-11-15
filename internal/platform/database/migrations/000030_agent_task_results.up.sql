CREATE TABLE agent_task_results (
    id TEXT PRIMARY KEY NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES collaboration_workspaces(id) ON DELETE CASCADE,
    task_id TEXT NOT NULL REFERENCES collaboration_tasks(id) ON DELETE CASCADE,
    assignment_id TEXT NOT NULL REFERENCES task_assignments(id) ON DELETE CASCADE,
    execution_id TEXT NOT NULL,
    recipient_agent_id TEXT NOT NULL REFERENCES agent_identities(id) ON DELETE CASCADE,
    result_json TEXT NOT NULL CHECK(json_valid(result_json)),
    received_at INTEGER NOT NULL,
    UNIQUE(assignment_id, recipient_agent_id)
);
CREATE INDEX idx_agent_task_results_recipient_received
ON agent_task_results(recipient_agent_id, received_at DESC, id);