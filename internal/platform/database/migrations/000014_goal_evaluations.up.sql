ALTER TABLE agent_goals ADD COLUMN completed_at INTEGER NOT NULL DEFAULT 0;

CREATE TABLE goal_evaluations (
    id TEXT PRIMARY KEY,
    goal_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    execution_status TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('satisfied', 'not_satisfied', 'inconclusive')),
    summary TEXT NOT NULL,
    criteria_json TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(goal_id, run_id),
    FOREIGN KEY(goal_id) REFERENCES agent_goals(id) ON DELETE CASCADE,
    FOREIGN KEY(run_id) REFERENCES plan_executions(run_id) ON DELETE CASCADE
);
CREATE INDEX idx_goal_evaluations_goal_created ON goal_evaluations(goal_id, created_at DESC);
