package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/execution"
)

// RequestCancel durably records cancellation intent before contacting a
// runtime. Repeated requests are idempotent; the execution remains active
// until an adapter observation confirms the actual outcome.
func (s *SQLiteStore) RequestCancel(ctx context.Context, executionID string) error {
	if executionID == "" {
		return dom.ErrInvalidEntity
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var status dom.LifecycleStatus
	if err = tx.QueryRowContext(ctx, "SELECT status FROM executions WHERE id=?", executionID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return dom.ErrNotFound
		}
		return err
	}
	if status != dom.LifecycleQueued && status != dom.LifecycleRunning {
		return dom.ErrInvalidTransition
	}

	now := time.Now().UTC()
	payload, err := json.Marshal(struct {
		Status dom.LifecycleStatus `json:"status_at_request"`
	}{Status: status})
	if err != nil {
		return fmt.Errorf("encode cancellation request: %w", err)
	}

	// The execution ID is the stable cancellation-request identity. A caller
	// may safely retry after a timeout without generating duplicate intent.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO execution_control_events (
			event_id,event_key,execution_id,event_type,actor_type,actor_id,source,payload_json,occurred_at
		) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(event_key) DO NOTHING
	`, "evt-cancel-requested-"+executionID, "execution.cancel_requested:"+executionID,
		executionID, "execution.cancel_requested", "system", "", "execution_manager",
		string(payload), now.UnixNano())
	if err != nil {
		return fmt.Errorf("persist cancellation request: %w", err)
	}
	return tx.Commit()
}
