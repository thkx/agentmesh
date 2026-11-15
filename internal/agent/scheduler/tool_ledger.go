package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type ToolLedger struct{ db *sql.DB }
type ToolInvocation struct {
	Result    json.RawMessage
	Error     string
	Completed bool
}

func NewToolLedger(db *sql.DB) (*ToolLedger, error) {
	if db == nil {
		return nil, errors.New("tool ledger database is nil")
	}
	return &ToolLedger{db: db}, nil
}

// Begin creates a durable intent before a side effect. Existing started records are never executed again.
func (l *ToolLedger) Begin(ctx context.Context, key, taskID, callID, tool string) (bool, error) {
	if key == "" || taskID == "" || callID == "" || tool == "" {
		return false, errors.New("invalid tool invocation identity")
	}
	now := time.Now().UTC().UnixNano()
	res, err := l.db.ExecContext(ctx, `INSERT OR IGNORE INTO agent_tool_invocations(idempotency_key,task_id,tool_call_id,tool_name,status,created_at,updated_at) VALUES(?,?,?,?,'started',?,?)`, key, taskID, callID, tool, now, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		return true, nil
	}
	var status string
	if err = l.db.QueryRowContext(ctx, `SELECT status FROM agent_tool_invocations WHERE idempotency_key=?`, key).Scan(&status); err != nil {
		return false, err
	}
	if status == "completed" {
		return false, nil
	}
	return false, ErrIndeterminate
}
func (l *ToolLedger) Complete(ctx context.Context, key string, result any, invokeErr error) error {
	var raw []byte
	var errText string
	if invokeErr != nil {
		errText = invokeErr.Error()
	} else {
		var err error
		raw, err = json.Marshal(result)
		if err != nil {
			return fmt.Errorf("encode tool result: %w", err)
		}
	}
	res, err := l.db.ExecContext(ctx, `UPDATE agent_tool_invocations SET status='completed',result_json=?,error_text=?,updated_at=? WHERE idempotency_key=? AND status='started'`, string(raw), errText, time.Now().UTC().UnixNano(), key)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrIndeterminate
	}
	return nil
}
func (l *ToolLedger) Load(ctx context.Context, key string) (json.RawMessage, string, bool, error) {
	var status, raw, errText sql.NullString
	err := l.db.QueryRowContext(ctx, `SELECT status,result_json,error_text FROM agent_tool_invocations WHERE idempotency_key=?`, key).Scan(&status, &raw, &errText)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", false, ErrNotFound
	}
	if err != nil {
		return nil, "", false, err
	}
	if status.String != "completed" {
		return nil, "", false, ErrIndeterminate
	}
	var result json.RawMessage
	if raw.Valid {
		result = json.RawMessage(raw.String)
	}
	errorText := ""
	if errText.Valid {
		errorText = errText.String
	}
	return result, errorText, true, nil
}
