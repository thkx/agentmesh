CREATE TABLE agent_task_states (
    task_id TEXT PRIMARY KEY NOT NULL,
    state_json TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_agent_task_states_updated_at ON agent_task_states(updated_at);
