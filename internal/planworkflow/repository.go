package planworkflow

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrGoalInactive = errors.New("goal is not active for plan execution")

type ExecutionRecord struct {
	GoalID          string    `json:"goal_id"`
	PlanID          string    `json:"plan_id"`
	PlanVersion     int       `json:"plan_version"`
	WorkflowID      string    `json:"workflow_id"`
	WorkflowVersion int       `json:"workflow_version"`
	RunID           string    `json:"run_id"`
	InputHash       string    `json:"input_hash"`
	CreatedAt       time.Time `json:"created_at"`
}

type Repository interface {
	Reserve(context.Context, ExecutionRecord) (ExecutionRecord, error)
}

type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) (*SQLiteRepository, error) {
	if db == nil {
		return nil, errors.New("plan execution database is nil")
	}
	return &SQLiteRepository{db: db}, nil
}

func (r *SQLiteRepository) Reserve(ctx context.Context, rec ExecutionRecord) (ExecutionRecord, error) {
	_, err := r.db.ExecContext(ctx, `INSERT INTO plan_executions(goal_id,plan_id,plan_version,workflow_id,workflow_version,run_id,input_hash,created_at)
SELECT ?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM agent_goals WHERE id=? AND status='active' AND completed_at=0)
ON CONFLICT(goal_id,plan_version) DO NOTHING`, rec.GoalID, rec.PlanID, rec.PlanVersion, rec.WorkflowID, rec.WorkflowVersion, rec.RunID, rec.InputHash, rec.CreatedAt.Unix(), rec.GoalID)
	if err != nil {
		return ExecutionRecord{}, err
	}
	var stored ExecutionRecord
	var created int64
	err = r.db.QueryRowContext(ctx, `SELECT goal_id,plan_id,plan_version,workflow_id,workflow_version,run_id,input_hash,created_at FROM plan_executions WHERE goal_id=? AND plan_version=?`, rec.GoalID, rec.PlanVersion).Scan(&stored.GoalID, &stored.PlanID, &stored.PlanVersion, &stored.WorkflowID, &stored.WorkflowVersion, &stored.RunID, &stored.InputHash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionRecord{}, ErrGoalInactive
	}
	if err != nil {
		return ExecutionRecord{}, err
	}
	stored.CreatedAt = time.Unix(created, 0).UTC()
	if stored.PlanID != rec.PlanID || stored.InputHash != rec.InputHash || stored.WorkflowID != rec.WorkflowID || stored.RunID != rec.RunID {
		return ExecutionRecord{}, ErrIdempotencyConflict
	}
	return stored, nil
}
