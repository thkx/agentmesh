package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	evaldom "github.com/thkx/agentmesh/internal/domain/goalevaluation"
)

type GoalEvaluationRepository struct{ db *sql.DB }

func NewGoalEvaluationRepository(db *sql.DB) (*GoalEvaluationRepository, error) {
	if db == nil {
		return nil, errors.New("goal evaluation database is nil")
	}
	return &GoalEvaluationRepository{db: db}, nil
}

func (r *GoalEvaluationRepository) FindExecution(ctx context.Context, goalID, runID string) (evaldom.ExecutionLink, error) {
	var link evaldom.ExecutionLink
	err := r.db.QueryRowContext(ctx, `SELECT goal_id,plan_id,plan_version,run_id FROM plan_executions WHERE goal_id=? AND run_id=?`, goalID, runID).Scan(&link.GoalID, &link.PlanID, &link.PlanVersion, &link.RunID)
	if errors.Is(err, sql.ErrNoRows) {
		return link, evaldom.ErrExecutionNotFound
	}
	return link, err
}

func (r *GoalEvaluationRepository) Get(ctx context.Context, goalID, runID string) (evaldom.Evaluation, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,goal_id,run_id,model_id,execution_status,status,summary,criteria_json,created_at FROM goal_evaluations WHERE goal_id=? AND run_id=?`, goalID, runID)
	return scanGoalEvaluation(row)
}

func (r *GoalEvaluationRepository) List(ctx context.Context, goalID string, limit int) ([]evaldom.Evaluation, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,goal_id,run_id,model_id,execution_status,status,summary,criteria_json,created_at FROM goal_evaluations WHERE goal_id=? ORDER BY created_at DESC,id LIMIT ?`, goalID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]evaldom.Evaluation, 0)
	for rows.Next() {
		item, scanErr := scanGoalEvaluation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *GoalEvaluationRepository) Save(ctx context.Context, evaluation evaldom.Evaluation, completeGoal bool) (evaldom.Evaluation, error) {
	criteria, err := json.Marshal(evaluation.Criteria)
	if err != nil {
		return evaldom.Evaluation{}, fmt.Errorf("marshal evaluation criteria: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return evaldom.Evaluation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, readErr := scanGoalEvaluation(tx.QueryRowContext(ctx, `SELECT id,goal_id,run_id,model_id,execution_status,status,summary,criteria_json,created_at FROM goal_evaluations WHERE goal_id=? AND run_id=?`, evaluation.GoalID, evaluation.RunID)); readErr == nil {
		if err = tx.Commit(); err != nil {
			return evaldom.Evaluation{}, err
		}
		return existing, nil
	} else if !errors.Is(readErr, evaldom.ErrNotFound) {
		return evaldom.Evaluation{}, readErr
	}
	var status string
	var completedAt int64
	if err = tx.QueryRowContext(ctx, `SELECT status,completed_at FROM agent_goals WHERE id=?`, evaluation.GoalID).Scan(&status, &completedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return evaldom.Evaluation{}, evaldom.ErrGoalNotActive
		}
		return evaldom.Evaluation{}, err
	}
	if status != "active" || completedAt > 0 {
		return evaldom.Evaluation{}, evaldom.ErrGoalNotActive
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO goal_evaluations(id,goal_id,run_id,model_id,execution_status,status,summary,criteria_json,created_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(goal_id,run_id) DO NOTHING`, evaluation.ID, evaluation.GoalID, evaluation.RunID, evaluation.ModelID, evaluation.ExecutionStatus, string(evaluation.Status), evaluation.Summary, string(criteria), evaluation.CreatedAt.UnixMilli())
	if err != nil {
		return evaldom.Evaluation{}, fmt.Errorf("insert goal evaluation: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return evaldom.Evaluation{}, err
	}
	if inserted == 0 {
		existing, readErr := scanGoalEvaluation(tx.QueryRowContext(ctx, `SELECT id,goal_id,run_id,model_id,execution_status,status,summary,criteria_json,created_at FROM goal_evaluations WHERE goal_id=? AND run_id=?`, evaluation.GoalID, evaluation.RunID))
		if readErr != nil {
			return evaldom.Evaluation{}, readErr
		}
		if err = tx.Commit(); err != nil {
			return evaldom.Evaluation{}, err
		}
		return existing, nil
	}
	if completeGoal {
		now := evaluation.CreatedAt.UnixMilli()
		result, updateErr := tx.ExecContext(ctx, `UPDATE agent_goals SET completed_at=?,updated_at=? WHERE id=? AND status='active' AND completed_at=0`, now, now, evaluation.GoalID)
		if updateErr != nil {
			return evaldom.Evaluation{}, updateErr
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			return evaldom.Evaluation{}, evaldom.ErrGoalNotActive
		}
	}
	if err = tx.Commit(); err != nil {
		return evaldom.Evaluation{}, err
	}
	return evaluation, nil
}

type scanner interface{ Scan(...any) error }

func scanGoalEvaluation(row scanner) (evaldom.Evaluation, error) {
	var item evaldom.Evaluation
	var status string
	var criteria string
	var created int64
	if err := row.Scan(&item.ID, &item.GoalID, &item.RunID, &item.ModelID, &item.ExecutionStatus, &status, &item.Summary, &criteria, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return item, evaldom.ErrNotFound
		}
		return item, err
	}
	if err := json.Unmarshal([]byte(criteria), &item.Criteria); err != nil {
		return item, fmt.Errorf("decode goal evaluation criteria: %w", err)
	}
	item.Status = evaldom.Status(status)
	item.CreatedAt = time.UnixMilli(created).UTC()
	return item, nil
}
