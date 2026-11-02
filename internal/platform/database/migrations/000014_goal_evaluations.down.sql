DROP INDEX IF EXISTS idx_goal_evaluations_goal_created;
DROP TABLE IF EXISTS goal_evaluations;
ALTER TABLE agent_goals DROP COLUMN completed_at;
