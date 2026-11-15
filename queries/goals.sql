-- name: CreateAgentGoal :exec
INSERT INTO agent_goals (id, description, constraints_json, success_criteria_json, context_json, status, created_at, updated_at, canceled_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAgentGoal :one
SELECT id, description, constraints_json, success_criteria_json, context_json, status, created_at, updated_at, canceled_at, completed_at
FROM agent_goals WHERE id = ?;

-- name: ListAgentGoals :many
SELECT id, description, constraints_json, success_criteria_json, context_json, status, created_at, updated_at, canceled_at, completed_at
FROM agent_goals ORDER BY created_at DESC, id LIMIT ?;

-- name: CancelAgentGoal :execrows
UPDATE agent_goals SET status = 'canceled', updated_at = ?, canceled_at = ?
WHERE id = ? AND status = 'active' AND completed_at = 0;

-- name: CreateAgentPlan :one
INSERT INTO agent_plans (
    id, goal_id, version, objective, steps_json, status, model_id, created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(goal_id),
    (SELECT COALESCE(MAX(version), 0) + 1 FROM agent_plans WHERE goal_id = sqlc.arg(goal_id)),
    sqlc.arg(objective),
    sqlc.arg(steps_json),
    sqlc.arg(status),
    sqlc.arg(model_id),
    sqlc.arg(created_at)
)
RETURNING version;

-- name: GetAgentPlan :one
SELECT id, goal_id, version, objective, steps_json, status, model_id, created_at
FROM agent_plans WHERE goal_id = ? AND version = ?;

-- name: GetLatestAgentPlan :one
SELECT id, goal_id, version, objective, steps_json, status, model_id, created_at
FROM agent_plans WHERE goal_id = ? ORDER BY version DESC LIMIT 1;

-- name: ListAgentPlans :many
SELECT id, goal_id, version, objective, steps_json, status, model_id, created_at
FROM agent_plans WHERE goal_id = ? ORDER BY version DESC LIMIT ?;

-- name: UpdateAgentGoal :execrows
UPDATE agent_goals
SET description = ?, constraints_json = ?, success_criteria_json = ?, context_json = ?, updated_at = ?
WHERE id = ? AND status = 'active' AND completed_at = 0;
