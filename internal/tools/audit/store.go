package audit

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/thkx/agentmesh/internal/tools"
)

type Store interface {
	Record(context.Context, tools.AuditEvent) error
	List(int) []tools.AuditEvent
}

type SQLite struct{ db *sql.DB }

func NewSQLite(db *sql.DB) (*SQLite, error) {
	if db == nil {
		return nil, errors.New("tool audit database is nil")
	}
	return &SQLite{db: db}, nil
}
func (s *SQLite) Record(ctx context.Context, e tools.AuditEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tool_execution_audit(id,tool_name,workspace,allowed,approved,authorization,started_at,duration_ns,error_text) VALUES(?,?,?,?,?,?,?,?,?)`, e.ID, e.Tool, e.Workspace, boolInt(e.Allowed), boolInt(e.Approved), e.Authorization, e.StartedAt.UTC().UnixNano(), int64(e.Duration), e.Error)
	return err
}
func (s *SQLite) List(limit int) []tools.AuditEvent {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,tool_name,workspace,allowed,approved,authorization,started_at,duration_ns,error_text FROM tool_execution_audit ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return []tools.AuditEvent{}
	}
	defer rows.Close()
	out := []tools.AuditEvent{}
	for rows.Next() {
		var e tools.AuditEvent
		var allowed, approved int
		var started, duration int64
		if rows.Scan(&e.ID, &e.Tool, &e.Workspace, &allowed, &approved, &e.Authorization, &started, &duration, &e.Error) == nil {
			e.Allowed = allowed == 1
			e.Approved = approved == 1
			e.StartedAt = time.Unix(0, started).UTC()
			e.Duration = time.Duration(duration)
			out = append(out, e)
		}
	}
	return out
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
