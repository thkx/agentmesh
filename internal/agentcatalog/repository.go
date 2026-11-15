package agentcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
)

var (
	ErrNotFound = errors.New("agent definition not found")
	ErrExists   = errors.New("agent definition already exists")
)

type Repository interface {
	Create(context.Context, dom.Definition) error
	Get(context.Context, string) (dom.Definition, error)
	List(context.Context) ([]dom.Definition, error)
	Update(context.Context, dom.Definition) error
	Delete(context.Context, string) error
	RecordRun(context.Context, RunRecord) error
	FinishRun(context.Context, string, dom.Result) error
	GetRun(context.Context, string) (RunRecord, error)
	ListRuns(context.Context, string, int) ([]RunRecord, error)
}

type RunRecord struct {
	ID           string      `json:"id"`
	DefinitionID string      `json:"definition_id"`
	TaskID       string      `json:"task_id"`
	Status       string      `json:"status"`
	Result       *dom.Result `json:"result,omitempty"`
	Error        string      `json:"error,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	FinishedAt   *time.Time  `json:"finished_at,omitempty"`
}

type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) (*SQLiteRepository, error) {
	if db == nil {
		return nil, errors.New("agent catalog database is nil")
	}
	return &SQLiteRepository{db: db}, nil
}
func (r *SQLiteRepository) Create(ctx context.Context, d dom.Definition) error {
	if err := d.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	d.CreatedAt, d.UpdatedAt, d.Version = now, now, 1
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_definitions(id,name,description,system_prompt,model_id,temperature,max_steps,max_tool_calls,max_output_tokens,timeout_seconds,approved_tools_json,enabled,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, d.ID, d.Name, d.Description, d.SystemPrompt, d.ModelID, d.Temperature, d.MaxSteps, d.MaxToolCalls, d.MaxOutputTokens, d.TimeoutSeconds, string(mustJSON(d.ApprovedTools)), boolInt(d.Enabled), d.Version, now.UnixNano(), now.UnixNano())
	if err != nil {
		if isUnique(err) {
			return ErrExists
		}
		return fmt.Errorf("insert agent definition: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO agent_definition_versions(definition_id,version,snapshot_json,created_at) VALUES(?,?,?,?)`, d.ID, d.Version, string(b), now.UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *SQLiteRepository) Get(ctx context.Context, id string) (dom.Definition, error) {
	var d dom.Definition
	var toolsJSON string
	var enabled int
	var created, updated int64
	err := r.db.QueryRowContext(ctx, `SELECT id,name,description,system_prompt,model_id,temperature,max_steps,max_tool_calls,max_output_tokens,timeout_seconds,approved_tools_json,enabled,version,created_at,updated_at FROM agent_definitions WHERE id=?`, id).Scan(&d.ID, &d.Name, &d.Description, &d.SystemPrompt, &d.ModelID, &d.Temperature, &d.MaxSteps, &d.MaxToolCalls, &d.MaxOutputTokens, &d.TimeoutSeconds, &toolsJSON, &enabled, &d.Version, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal([]byte(toolsJSON), &d.ApprovedTools); err != nil {
		return d, err
	}
	d.Enabled = enabled == 1
	d.CreatedAt = time.Unix(0, created).UTC()
	d.UpdatedAt = time.Unix(0, updated).UTC()
	return d, nil
}
func (r *SQLiteRepository) List(ctx context.Context) ([]dom.Definition, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM agent_definitions ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]dom.Definition, 0, len(ids))
	for _, id := range ids {
		d, e := r.Get(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, nil
}
func (r *SQLiteRepository) Update(ctx context.Context, d dom.Definition) error {
	if err := d.Validate(); err != nil {
		return err
	}
	old, err := r.Get(ctx, d.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	d.CreatedAt = old.CreatedAt
	d.UpdatedAt = now
	d.Version = old.Version + 1
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE agent_definitions SET name=?,description=?,system_prompt=?,model_id=?,temperature=?,max_steps=?,max_tool_calls=?,max_output_tokens=?,timeout_seconds=?,approved_tools_json=?,enabled=?,version=?,updated_at=? WHERE id=? AND version=?`, d.Name, d.Description, d.SystemPrompt, d.ModelID, d.Temperature, d.MaxSteps, d.MaxToolCalls, d.MaxOutputTokens, d.TimeoutSeconds, string(mustJSON(d.ApprovedTools)), boolInt(d.Enabled), d.Version, now.UnixNano(), d.ID, old.Version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("agent definition changed concurrently")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO agent_definition_versions(definition_id,version,snapshot_json,created_at) VALUES(?,?,?,?)`, d.ID, d.Version, string(b), now.UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *SQLiteRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM agent_definitions WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (r *SQLiteRepository) RecordRun(ctx context.Context, v RunRecord) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO agent_runs(id,definition_id,task_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, v.ID, v.DefinitionID, v.TaskID, v.Status, v.CreatedAt.UnixNano(), v.CreatedAt.UnixNano())
	return err
}
func (r *SQLiteRepository) FinishRun(ctx context.Context, id string, result dom.Result) error {
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE agent_runs SET status=?,result_json=?,error_text=?,finished_at=?,updated_at=? WHERE id=?`, string(result.Status), string(b), result.Error, result.FinishedAt.UnixNano(), time.Now().UTC().UnixNano(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func isUnique(err error) bool {
	return err != nil && (contains(err.Error(), "UNIQUE constraint"))
}
func contains(s, sub string) bool {
	return len(s) >= len(sub) && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}

func (r *SQLiteRepository) GetRun(ctx context.Context, id string) (RunRecord, error) {
	var v RunRecord
	var resultJSON sql.NullString
	var finished sql.NullInt64
	var created int64
	err := r.db.QueryRowContext(ctx, `SELECT id,definition_id,task_id,status,result_json,error_text,created_at,finished_at FROM agent_runs WHERE id=?`, id).Scan(&v.ID, &v.DefinitionID, &v.TaskID, &v.Status, &resultJSON, &v.Error, &created, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	v.CreatedAt = time.Unix(0, created).UTC()
	if finished.Valid {
		t := time.Unix(0, finished.Int64).UTC()
		v.FinishedAt = &t
	}
	if resultJSON.Valid {
		var result dom.Result
		if err = json.Unmarshal([]byte(resultJSON.String), &result); err != nil {
			return v, err
		}
		v.Result = &result
	}
	return v, nil
}
func (r *SQLiteRepository) ListRuns(ctx context.Context, definitionID string, limit int) ([]RunRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM agent_runs WHERE definition_id=? ORDER BY created_at DESC LIMIT ?`, definitionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]RunRecord, 0, len(ids))
	for _, id := range ids {
		v, e := r.GetRun(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
