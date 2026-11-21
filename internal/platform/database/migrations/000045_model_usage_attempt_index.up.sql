CREATE INDEX idx_model_usage_events_attempt ON model_usage_events(attempt_id) WHERE attempt_id IS NOT NULL;
