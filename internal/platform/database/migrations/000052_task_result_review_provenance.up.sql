ALTER TABLE agent_task_result_assessments
    ADD COLUMN review_method TEXT NOT NULL DEFAULT 'legacy'
        CHECK(review_method IN ('legacy','policy','model')),
    ADD COLUMN reviewer_model_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN reviewer_provider_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN model_response_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN model_invoked INTEGER NOT NULL DEFAULT 0 CHECK(model_invoked IN (0,1)),
    ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(input_tokens >= 0),
    ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0 CHECK(output_tokens >= 0);

CREATE INDEX idx_agent_task_result_assessments_reviewer_model
    ON agent_task_result_assessments(reviewer_model_id, created_at DESC)
    WHERE reviewer_model_id <> '';
