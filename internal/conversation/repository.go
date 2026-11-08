package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Repository interface {
	Create(context.Context, Conversation) error
	Get(context.Context, string) (Conversation, error)
	List(context.Context, Scope, string, int) ([]Conversation, error)
	Append(context.Context, Message) (Message, error)
	Messages(context.Context, string, int64, int) ([]Message, error)
	SaveSummary(context.Context, string, int64, int64, string, int) error
	LatestSummary(context.Context, string) (string, int64, error)
	Archive(context.Context, string, time.Time) error
}
type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) (*SQLiteRepository, error) {
	if db == nil {
		return nil, errors.New("conversation database is nil")
	}
	return &SQLiteRepository{db: db}, nil
}
func (r *SQLiteRepository) Create(ctx context.Context, c Conversation) error {
	if err := c.Validate(); err != nil {
		return err
	}
	meta, _ := json.Marshal(c.Metadata)
	now := c.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	updated := c.UpdatedAt
	if updated.IsZero() {
		updated = now
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO conversations(id,title,scope_type,scope_id,metadata_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, c.ID, c.Title, c.ScopeType, c.ScopeID, string(meta), now.UnixMilli(), updated.UnixMilli())
	return err
}
func (r *SQLiteRepository) Get(ctx context.Context, id string) (Conversation, error) {
	var c Conversation
	var scope, meta string
	var created, updated, archived int64
	err := r.db.QueryRowContext(ctx, `SELECT id,title,scope_type,scope_id,metadata_json,created_at,updated_at,archived_at FROM conversations WHERE id=?`, id).Scan(&c.ID, &c.Title, &scope, &c.ScopeID, &meta, &created, &updated, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	c.ScopeType = Scope(scope)
	_ = json.Unmarshal([]byte(meta), &c.Metadata)
	c.CreatedAt = time.UnixMilli(created).UTC()
	c.UpdatedAt = time.UnixMilli(updated).UTC()
	if archived > 0 {
		c.ArchivedAt = time.UnixMilli(archived).UTC()
	}
	return c, nil
}
func (r *SQLiteRepository) List(ctx context.Context, scope Scope, scopeID string, limit int) ([]Conversation, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	q := `SELECT id,title,scope_type,scope_id,metadata_json,created_at,updated_at,archived_at FROM conversations WHERE archived_at=0`
	args := []any{}
	if scope != "" {
		q += ` AND scope_type=?`
		args = append(args, scope)
	}
	if scopeID != "" {
		q += ` AND scope_id=?`
		args = append(args, scopeID)
	}
	q += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Conversation{}
	for rows.Next() {
		var c Conversation
		var st, meta string
		var a, u, x int64
		if err = rows.Scan(&c.ID, &c.Title, &st, &c.ScopeID, &meta, &a, &u, &x); err != nil {
			return nil, err
		}
		c.ScopeType = Scope(st)
		_ = json.Unmarshal([]byte(meta), &c.Metadata)
		c.CreatedAt = time.UnixMilli(a).UTC()
		c.UpdatedAt = time.UnixMilli(u).UTC()
		if x > 0 {
			c.ArchivedAt = time.UnixMilli(x).UTC()
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (r *SQLiteRepository) Append(ctx context.Context, m Message) (Message, error) {
	if err := m.Validate(); err != nil {
		return m, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	var archived int64
	if err = tx.QueryRowContext(ctx, `SELECT archived_at FROM conversations WHERE id=?`, m.ConversationID).Scan(&archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return m, ErrNotFound
		}
		return m, err
	}
	if archived != 0 {
		return m, errors.New("conversation is archived")
	}
	if m.Sequence == 0 {
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM conversation_messages WHERE conversation_id=?`, m.ConversationID).Scan(&m.Sequence); err != nil {
			return m, err
		}
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	meta, _ := json.Marshal(m.Metadata)
	if m.TokenEstimate <= 0 {
		m.TokenEstimate = (len([]rune(m.Content)) + 3) / 4
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO conversation_messages(id,conversation_id,sequence,role,name,content,tool_calls_json,tool_call_id,token_estimate,metadata_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, m.ID, m.ConversationID, m.Sequence, m.Role, m.Name, m.Content, m.ToolCallsJSON, m.ToolCallID, m.TokenEstimate, string(meta), m.CreatedAt.UnixMilli())
	if err != nil {
		return m, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE conversations SET updated_at=? WHERE id=?`, m.CreatedAt.UnixMilli(), m.ConversationID)
	if err != nil {
		return m, err
	}
	if err = tx.Commit(); err != nil {
		return m, err
	}
	return m, nil
}
func (r *SQLiteRepository) Messages(ctx context.Context, id string, after int64, limit int) ([]Message, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,conversation_id,sequence,role,name,content,tool_calls_json,tool_call_id,token_estimate,metadata_json,created_at FROM conversation_messages WHERE conversation_id=? AND sequence>? ORDER BY sequence LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var role, meta string
		var ts int64
		if err = rows.Scan(&m.ID, &m.ConversationID, &m.Sequence, &role, &m.Name, &m.Content, &m.ToolCallsJSON, &m.ToolCallID, &m.TokenEstimate, &meta, &ts); err != nil {
			return nil, err
		}
		m.Role = role
		_ = json.Unmarshal([]byte(meta), &m.Metadata)
		m.CreatedAt = time.UnixMilli(ts).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}
func (r *SQLiteRepository) SaveSummary(ctx context.Context, id string, from, to int64, summary string, tokens int) error {
	if from < 1 || to < from || summary == "" {
		return ErrInvalid
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO conversation_summaries(id,conversation_id,from_sequence,to_sequence,summary,token_estimate,created_at) VALUES(lower(hex(randomblob(16))),?,?,?,?,?,?)`, id, from, to, summary, tokens, time.Now().UTC().UnixMilli())
	return err
}
func (r *SQLiteRepository) LatestSummary(ctx context.Context, id string) (string, int64, error) {
	var s string
	var seq int64
	err := r.db.QueryRowContext(ctx, `SELECT summary,to_sequence FROM conversation_summaries WHERE conversation_id=? ORDER BY to_sequence DESC LIMIT 1`, id).Scan(&s, &seq)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	return s, seq, err
}
func (r *SQLiteRepository) Archive(ctx context.Context, id string, at time.Time) error {
	res, err := r.db.ExecContext(ctx, `UPDATE conversations SET archived_at=?,updated_at=? WHERE id=? AND archived_at=0`, at.UnixMilli(), at.UnixMilli(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
