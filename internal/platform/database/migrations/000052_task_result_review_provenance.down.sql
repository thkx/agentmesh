DROP INDEX IF EXISTS idx_agent_task_result_assessments_reviewer_model;

ALTER TABLE agent_task_result_assessments
    DROP COLUMN output_tokens;
ALTER TABLE agent_task_result_assessments
    DROP COLUMN input_tokens;
ALTER TABLE agent_task_result_assessments
    DROP COLUMN model_invoked;
ALTER TABLE agent_task_result_assessments
    DROP COLUMN model_response_id;
ALTER TABLE agent_task_result_assessments
    DROP COLUMN reviewer_provider_id;
ALTER TABLE agent_task_result_assessments
    DROP COLUMN reviewer_model_id;
ALTER TABLE agent_task_result_assessments
    DROP COLUMN review_method;
