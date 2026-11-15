package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/reflection"
)

type ReflectionRepository struct{ db *sql.DB }

func NewReflectionRepository(db *sql.DB) (*ReflectionRepository, error) {
	if db == nil {
		return nil, errors.New("reflection database is nil")
	}
	return &ReflectionRepository{db: db}, nil
}
func (r *ReflectionRepository) Get(ctx context.Context, goalID, runID string) (dom.Reflection, error) {
	return scanReflection(r.db.QueryRowContext(ctx, `SELECT id,goal_id,run_id,evaluation_id,model_id,summary,insights_json,created_at FROM goal_reflections WHERE goal_id=? AND run_id=?`, goalID, runID))
}
func (r *ReflectionRepository) List(ctx context.Context, goalID string, limit int) ([]dom.Reflection, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,goal_id,run_id,evaluation_id,model_id,summary,insights_json,created_at FROM goal_reflections WHERE goal_id=? ORDER BY created_at DESC,id LIMIT ?`, goalID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]dom.Reflection, 0)
	for rows.Next() {
		item, e := scanReflection(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (r *ReflectionRepository) Save(ctx context.Context, item dom.Reflection) (dom.Reflection, error) {
	raw, err := json.Marshal(item.Insights)
	if err != nil {
		return dom.Reflection{}, fmt.Errorf("marshal reflection insights: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO goal_reflections(id,goal_id,run_id,evaluation_id,model_id,summary,insights_json,created_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(goal_id,run_id) DO NOTHING`, item.ID, item.GoalID, item.RunID, item.EvaluationID, item.ModelID, item.Summary, string(raw), item.CreatedAt.UnixMilli())
	if err != nil {
		return dom.Reflection{}, fmt.Errorf("save reflection: %w", err)
	}
	return r.Get(ctx, item.GoalID, item.RunID)
}

type reflectionScanner interface{ Scan(...any) error }

func scanReflection(row reflectionScanner) (dom.Reflection, error) {
	var item dom.Reflection
	var raw string
	var created int64
	if err := row.Scan(&item.ID, &item.GoalID, &item.RunID, &item.EvaluationID, &item.ModelID, &item.Summary, &raw, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return item, dom.ErrNotFound
		}
		return item, err
	}
	if err := json.Unmarshal([]byte(raw), &item.Insights); err != nil {
		return item, fmt.Errorf("decode reflection insights: %w", err)
	}
	item.CreatedAt = time.UnixMilli(created).UTC()
	return item, nil
}
