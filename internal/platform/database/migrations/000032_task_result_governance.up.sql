ALTER TABLE agent_task_result_assessments ADD COLUMN reviewer_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_task_result_assessments ADD COLUMN decision_note TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_task_result_assessments ADD COLUMN reviewed_at INTEGER;
ALTER TABLE task_assignments ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0 CHECK(retry_count >= 0);
ALTER TABLE task_assignments ADD COLUMN max_retries INTEGER NOT NULL DEFAULT 3 CHECK(max_retries BETWEEN 0 AND 20);
ALTER TABLE task_assignments ADD COLUMN failure_reason TEXT NOT NULL DEFAULT '';
CREATE TABLE agent_task_result_review_audit (
 id TEXT PRIMARY KEY NOT NULL,
 assessment_id TEXT NOT NULL REFERENCES agent_task_result_assessments(id) ON DELETE RESTRICT,
 result_id TEXT NOT NULL REFERENCES agent_task_results(id) ON DELETE RESTRICT,
 workspace_id TEXT NOT NULL REFERENCES collaboration_workspaces(id) ON DELETE RESTRICT,
 task_id TEXT NOT NULL REFERENCES collaboration_tasks(id) ON DELETE RESTRICT,
 assignment_id TEXT NOT NULL REFERENCES task_assignments(id) ON DELETE RESTRICT,
 recipient_agent_id TEXT NOT NULL REFERENCES agent_identities(id) ON DELETE RESTRICT,
 reviewer_id TEXT NOT NULL,
 outcome TEXT NOT NULL CHECK(outcome IN ('accepted','rejected')),
 note TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL
);
CREATE INDEX idx_task_result_review_audit_result ON agent_task_result_review_audit(recipient_agent_id,assignment_id,created_at);
