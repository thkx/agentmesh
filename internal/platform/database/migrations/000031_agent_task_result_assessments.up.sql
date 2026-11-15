CREATE TABLE agent_task_result_assessments (
    id TEXT PRIMARY KEY NOT NULL,
    result_id TEXT NOT NULL REFERENCES agent_task_results(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES collaboration_workspaces(id) ON DELETE CASCADE,
    task_id TEXT NOT NULL REFERENCES collaboration_tasks(id) ON DELETE CASCADE,
    assignment_id TEXT NOT NULL REFERENCES task_assignments(id) ON DELETE CASCADE,
    recipient_agent_id TEXT NOT NULL REFERENCES agent_identities(id) ON DELETE CASCADE,
    outcome TEXT NOT NULL CHECK(outcome IN ('accepted','rejected','human_review')),
    reason TEXT NOT NULL DEFAULT '',
    confidence REAL NOT NULL CHECK(confidence >= 0 AND confidence <= 1),
    evidence_json TEXT NOT NULL CHECK(json_valid(evidence_json)),
    created_at INTEGER NOT NULL,
    UNIQUE(result_id, recipient_agent_id)
);
CREATE INDEX idx_agent_task_result_assessments_task ON agent_task_result_assessments(task_id, created_at DESC);
