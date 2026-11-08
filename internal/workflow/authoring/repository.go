package authoring

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/workflow/compiler"
	"time"
)

type Template struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Definition  workflow.Definition `json:"definition"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}
type Version struct {
	WorkflowID string    `json:"workflow_id"`
	Version    int       `json:"version"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
}
type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("workflow authoring database is nil")
	}
	return &Repository{db: db}, nil
}
func (r *Repository) SaveTemplate(ctx context.Context, t Template) error {
	if t.ID == "" || t.Name == "" {
		return errors.New("template id and name are required")
	}
	if _, err := compiler.Compile(t.Definition); err != nil {
		return err
	}
	b, err := json.Marshal(t.Definition)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = r.db.ExecContext(ctx, `INSERT INTO workflow_templates(id,name,description,definition_json,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,definition_json=excluded.definition_json,updated_at=excluded.updated_at`, t.ID, t.Name, t.Description, string(b), now.UnixMilli(), now.UnixMilli())
	return err
}
func (r *Repository) GetTemplate(ctx context.Context, id string) (Template, error) {
	var t Template
	var b string
	var c, u int64
	err := r.db.QueryRowContext(ctx, `SELECT id,name,description,definition_json,created_at,updated_at FROM workflow_templates WHERE id=?`, id).Scan(&t.ID, &t.Name, &t.Description, &b, &c, &u)
	if err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(b), &t.Definition); err != nil {
		return t, err
	}
	t.CreatedAt = time.UnixMilli(c).UTC()
	t.UpdatedAt = time.UnixMilli(u).UTC()
	return t, nil
}
func (r *Repository) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name,description,definition_json,created_at,updated_at FROM workflow_templates ORDER BY updated_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		var t Template
		var b string
		var c, u int64
		if err = rows.Scan(&t.ID, &t.Name, &t.Description, &b, &c, &u); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &t.Definition); err != nil {
			return nil, err
		}
		t.CreatedAt = time.UnixMilli(c).UTC()
		t.UpdatedAt = time.UnixMilli(u).UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}
func (r *Repository) DeleteTemplate(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM workflow_templates WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (r *Repository) Versions(ctx context.Context, id string) ([]Version, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,version,name,created_at FROM workflows WHERE id=? ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var v Version
		var ts int64
		if err = rows.Scan(&v.WorkflowID, &v.Version, &v.Name, &ts); err != nil {
			return nil, err
		}
		v.CreatedAt = time.Unix(ts, 0).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}
