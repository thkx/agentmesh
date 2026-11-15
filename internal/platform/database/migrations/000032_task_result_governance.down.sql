DROP INDEX IF EXISTS idx_task_result_review_audit_result;
DROP TABLE IF EXISTS agent_task_result_review_audit;
ALTER TABLE task_assignments DROP COLUMN failure_reason;
ALTER TABLE task_assignments DROP COLUMN max_retries;
ALTER TABLE task_assignments DROP COLUMN retry_count;
ALTER TABLE agent_task_result_assessments DROP COLUMN reviewed_at;
ALTER TABLE agent_task_result_assessments DROP COLUMN decision_note;
ALTER TABLE agent_task_result_assessments DROP COLUMN reviewer_id;
