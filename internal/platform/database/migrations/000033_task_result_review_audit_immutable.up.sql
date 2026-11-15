CREATE TRIGGER agent_task_result_review_audit_no_update
BEFORE UPDATE ON agent_task_result_review_audit
BEGIN
    SELECT RAISE(ABORT, 'task result review audit is append-only');
END;

CREATE TRIGGER agent_task_result_review_audit_no_delete
BEFORE DELETE ON agent_task_result_review_audit
BEGIN
    SELECT RAISE(ABORT, 'task result review audit is append-only');
END;
