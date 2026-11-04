CREATE TABLE plan_executions (
    goal_id TEXT NOT NULL,
    plan_id TEXT NOT NULL,
    plan_version INTEGER NOT NULL CHECK (plan_version > 0),
    workflow_id TEXT NOT NULL,
    workflow_version INTEGER NOT NULL CHECK (workflow_version > 0),
    run_id TEXT NOT NULL UNIQUE,
    input_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (goal_id, plan_version),
    FOREIGN KEY (goal_id) REFERENCES agent_goals(id) ON DELETE CASCADE,
    FOREIGN KEY (plan_id) REFERENCES agent_plans(id) ON DELETE CASCADE
);
CREATE INDEX idx_plan_executions_run ON plan_executions(run_id);
