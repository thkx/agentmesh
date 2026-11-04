CREATE TABLE agent_goals (
    id TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    constraints_json TEXT NOT NULL DEFAULT '[]',
    success_criteria_json TEXT NOT NULL DEFAULT '[]',
    context_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL CHECK (status IN ('active', 'canceled')),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    canceled_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_agent_goals_status_updated ON agent_goals(status, updated_at DESC);

CREATE TABLE agent_plans (
    id TEXT PRIMARY KEY,
    goal_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    objective TEXT NOT NULL,
    steps_json TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('validated')),
    model_id TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    FOREIGN KEY(goal_id) REFERENCES agent_goals(id) ON DELETE CASCADE,
    UNIQUE(goal_id, version)
);
CREATE INDEX idx_agent_plans_goal_version ON agent_plans(goal_id, version DESC);
