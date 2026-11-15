package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/memory"
)

type MemoryRepository struct{ db *sql.DB }

func NewMemoryRepository(db *sql.DB) (*MemoryRepository, error) {
	if db == nil {
		return nil, errors.New("memory database is nil")
	}
	return &MemoryRepository{db: db}, nil
}

func (r *MemoryRepository) IngestCandidates(ctx context.Context, items []dom.Memory) ([]dom.Memory, error) {
	if len(items) == 0 {
		return []dom.Memory{}, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return nil, err
		}
		evidence, err := json.Marshal(item.Evidence)
		if err != nil {
			return nil, err
		}
		tags, err := json.Marshal(item.Tags)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO agent_memories(id,memory_type,status,scope_type,scope_id,goal_id,source_reflection_id,source_run_id,source_index,title,content,recommendation,confidence,evidence_json,tags_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_reflection_id,source_index) DO NOTHING`, item.ID, item.Type, item.Status, item.ScopeType, item.ScopeID, item.GoalID, item.SourceReflectionID, item.SourceRunID, item.SourceIndex, item.Title, item.Content, item.Recommendation, item.Confidence, string(evidence), string(tags), item.CreatedAt.UnixMilli(), item.UpdatedAt.UnixMilli())
		if err != nil {
			return nil, fmt.Errorf("insert memory candidate: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	out := make([]dom.Memory, 0, len(items))
	for _, item := range items {
		stored, err := r.getBySource(ctx, item.SourceReflectionID, item.SourceIndex)
		if err != nil {
			return nil, err
		}
		out = append(out, stored)
	}
	return out, nil
}

func (r *MemoryRepository) Get(ctx context.Context, id string) (dom.Memory, error) {
	return scanMemory(r.db.QueryRowContext(ctx, memorySelect+` WHERE id=?`, id))
}
func (r *MemoryRepository) getBySource(ctx context.Context, sourceID string, index int) (dom.Memory, error) {
	return scanMemory(r.db.QueryRowContext(ctx, memorySelect+` WHERE source_reflection_id=? AND source_index=?`, sourceID, index))
}
func (r *MemoryRepository) ListByGoal(ctx context.Context, goalID string, limit int) ([]dom.Memory, error) {
	rows, err := r.db.QueryContext(ctx, memorySelect+` WHERE goal_id=? ORDER BY created_at DESC,id LIMIT ?`, goalID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]dom.Memory, 0)
	for rows.Next() {
		item, e := scanMemory(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (r *MemoryRepository) Promote(ctx context.Context, id string, typ dom.Type, scope dom.ScopeType, scopeID, reason string, now time.Time) (dom.Memory, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE agent_memories SET memory_type=?,status='active',scope_type=?,scope_id=?,promotion_reason=?,promoted_at=?,updated_at=? WHERE id=? AND status='candidate'`, typ, scope, scopeID, reason, now.UnixMilli(), now.UnixMilli(), id)
	if err != nil {
		return dom.Memory{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return dom.Memory{}, err
	}
	if n == 0 {
		item, getErr := r.Get(ctx, id)
		if getErr != nil {
			return dom.Memory{}, getErr
		}
		return dom.Memory{}, fmt.Errorf("%w: current status is %s", dom.ErrInvalidState, item.Status)
	}
	return r.Get(ctx, id)
}
func (r *MemoryRepository) Archive(ctx context.Context, id string, now time.Time) (dom.Memory, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE agent_memories SET status='archived',archived_at=?,updated_at=? WHERE id=? AND status='active'`, now.UnixMilli(), now.UnixMilli(), id)
	if err != nil {
		return dom.Memory{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return dom.Memory{}, err
	}
	if n == 0 {
		item, e := r.Get(ctx, id)
		if e != nil {
			return dom.Memory{}, e
		}
		return dom.Memory{}, fmt.Errorf("%w: current status is %s", dom.ErrInvalidState, item.Status)
	}
	return r.Get(ctx, id)
}
func (r *MemoryRepository) Reject(ctx context.Context, id, reason string, now time.Time) (dom.Memory, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE agent_memories SET status='rejected',rejection_reason=?,updated_at=? WHERE id=? AND status='candidate'`, reason, now.UnixMilli(), id)
	if err != nil {
		return dom.Memory{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return dom.Memory{}, err
	}
	if n == 0 {
		item, e := r.Get(ctx, id)
		if e != nil {
			return dom.Memory{}, e
		}
		return dom.Memory{}, fmt.Errorf("%w: current status is %s", dom.ErrInvalidState, item.Status)
	}
	return r.Get(ctx, id)
}
func (r *MemoryRepository) SearchActive(ctx context.Context, goalID string, agentIDs []string, limit int) ([]dom.Memory, error) {
	clauses := []string{`(scope_type='global')`}
	args := []any{}
	if strings.TrimSpace(goalID) != "" {
		clauses = append(clauses, `(scope_type='goal' AND scope_id=?)`)
		args = append(args, goalID)
	}
	if len(agentIDs) > 0 {
		marks := make([]string, len(agentIDs))
		for i, id := range agentIDs {
			marks[i] = "?"
			args = append(args, id)
		}
		clauses = append(clauses, `(scope_type='agent' AND scope_id IN (`+strings.Join(marks, ",")+`))`)
	}
	args = append([]any{dom.StatusActive}, args...)
	args = append(args, limit)
	query := memorySelect + ` WHERE status=? AND (` + strings.Join(clauses, " OR ") + `) ORDER BY confidence DESC,updated_at DESC LIMIT ?`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]dom.Memory, 0)
	for rows.Next() {
		item, e := scanMemory(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (r *MemoryRepository) Touch(ctx context.Context, ids []string, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `UPDATE agent_memories SET last_accessed_at=?,access_count=access_count+1 WHERE id=? AND status='active'`, now.UnixMilli(), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const memorySelect = `SELECT id,memory_type,status,scope_type,scope_id,goal_id,source_reflection_id,source_run_id,source_index,title,content,recommendation,confidence,evidence_json,tags_json,promotion_reason,rejection_reason,created_at,updated_at,promoted_at,archived_at,last_accessed_at,access_count FROM agent_memories`

type memoryScanner interface{ Scan(...any) error }

func scanMemory(row memoryScanner) (dom.Memory, error) {
	var m dom.Memory
	var evidence, tags string
	var created, updated, promoted, archived, last int64
	err := row.Scan(&m.ID, &m.Type, &m.Status, &m.ScopeType, &m.ScopeID, &m.GoalID, &m.SourceReflectionID, &m.SourceRunID, &m.SourceIndex, &m.Title, &m.Content, &m.Recommendation, &m.Confidence, &evidence, &tags, &m.PromotionReason, &m.RejectionReason, &created, &updated, &promoted, &archived, &last, &m.AccessCount)
	if errors.Is(err, sql.ErrNoRows) {
		return m, dom.ErrNotFound
	}
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal([]byte(evidence), &m.Evidence); err != nil {
		return m, fmt.Errorf("decode memory evidence: %w", err)
	}
	if err = json.Unmarshal([]byte(tags), &m.Tags); err != nil {
		return m, fmt.Errorf("decode memory tags: %w", err)
	}
	m.CreatedAt = time.UnixMilli(created).UTC()
	m.UpdatedAt = time.UnixMilli(updated).UTC()
	if promoted > 0 {
		m.PromotedAt = time.UnixMilli(promoted).UTC()
	}
	if archived > 0 {
		m.ArchivedAt = time.UnixMilli(archived).UTC()
	}
	if last > 0 {
		m.LastAccessedAt = time.UnixMilli(last).UTC()
	}
	return m, nil
}
