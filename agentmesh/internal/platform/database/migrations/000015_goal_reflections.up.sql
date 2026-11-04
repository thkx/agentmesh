CREATE TABLE goal_reflections (
    id TEXT PRIMARY KEY,
    goal_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    evaluation_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    summary TEXT NOT NULL,
    insights_json TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(goal_id, run_id),
    FOREIGN KEY(goal_id, run_id) REFERENCES goal_evaluations(goal_id, run_id) ON DELETE RESTRICT
);
CREATE INDEX idx_goal_reflections_goal_created ON goal_reflections(goal_id, created_at DESC);
CREATE TRIGGER goal_reflections_no_update BEFORE UPDATE ON goal_reflections BEGIN SELECT RAISE(ABORT, 'goal reflections are append-only'); END;
CREATE TRIGGER goal_reflections_no_delete BEFORE DELETE ON goal_reflections BEGIN SELECT RAISE(ABORT, 'goal reflections are append-only'); END;
