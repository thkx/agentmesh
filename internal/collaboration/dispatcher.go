package collaboration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Dispatcher delivers only events with an explicit, validated recipient rule.
// Unknown event types are marked unroutable; they are never broadcast to a workspace.
const maxOutboxAttempts = 8

type Dispatcher struct {
	db       *sql.DB
	interval time.Duration
}

func NewDispatcher(db *sql.DB, interval time.Duration) (*Dispatcher, error) {
	if db == nil {
		return nil, errors.New("collaboration dispatcher database is nil")
	}
	if interval <= 0 {
		interval = time.Second
	}
	return &Dispatcher{db: db, interval: interval}, nil
}

func (d *Dispatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		if err := d.DispatchOnce(ctx, 100); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.ErrorContext(ctx, "collaboration outbox dispatch batch had failures", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *Dispatcher) DispatchOnce(ctx context.Context, limit int) error {
	if err := d.expireApprovals(ctx, 100); err != nil {
		return err
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := d.db.QueryContext(ctx, "SELECT id FROM collaboration_outbox WHERE published_at IS NULL AND next_attempt_at<=? ORDER BY created_at,id LIMIT ?", time.Now().UTC().UnixMilli(), limit)
	if err != nil {
		return err
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	var batchErr error
	for _, id := range ids {
		if dispatchErr := d.dispatchOne(ctx, id); dispatchErr != nil {
			batchErr = errors.Join(batchErr, dispatchErr)
		}
	}
	return batchErr
}

func (d *Dispatcher) dispatchOne(ctx context.Context, id string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var event Event
	var payload string
	var created int64
	err = tx.QueryRowContext(ctx, "SELECT id,workspace_id,aggregate_type,aggregate_id,event_type,payload_json,created_at FROM collaboration_outbox WHERE id=? AND published_at IS NULL", id).
		Scan(&event.ID, &event.WorkspaceID, &event.AggregateType, &event.AggregateID, &event.Type, &payload, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !json.Valid([]byte(payload)) {
		cause := fmt.Errorf("outbox event %s has invalid JSON payload", id)
		_ = tx.Rollback()
		return d.recordDispatchFailure(ctx, id, cause)
	}
	event.Payload = json.RawMessage(payload)
	event.CreatedAt = time.UnixMilli(created).UTC()

	recipients, routeErr := routeRecipients(ctx, tx, event)
	if routeErr != nil {
		_ = tx.Rollback()
		return d.recordDispatchFailure(ctx, id, routeErr)
	}
	now := time.Now().UTC().UnixMilli()
	for _, agentID := range recipients {
		_, err = tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO collaboration_inbox(id,agent_id,event_id,event_type,payload_json,status,received_at) VALUES(?,?,?,?,?,'pending',?)",
			event.ID+":"+agentID, agentID, event.ID, event.Type, payload, now)
		if err != nil {
			_ = tx.Rollback()
			return d.recordDispatchFailure(ctx, id, err)
		}
	}
	lastError := ""
	if len(recipients) == 0 {
		lastError = "unroutable: no explicit recipient rule or eligible recipient"
	}
	_, err = tx.ExecContext(ctx,
		"UPDATE collaboration_outbox SET published_at=?,attempts=attempts+1,last_error=?,next_attempt_at=0 WHERE id=? AND published_at IS NULL",
		now, lastError, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

type expiredApproval struct{ id, workspaceID, requesterID string }

func (d *Dispatcher) expireApprovals(ctx context.Context, limit int) error {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	rows, err := tx.QueryContext(ctx, `SELECT id,workspace_id,COALESCE(requester_agent_id,'')
		FROM collaboration_approvals WHERE status='pending' AND expires_at<=?
		ORDER BY expires_at,id LIMIT ?`, now.UnixMilli(), limit)
	if err != nil {
		return err
	}
	items := make([]expiredApproval, 0)
	for rows.Next() {
		var item expiredApproval
		if err = rows.Scan(&item.id, &item.workspaceID, &item.requesterID); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		res, updateErr := tx.ExecContext(ctx, `UPDATE collaboration_approvals SET status='expired',decided_at=?
			WHERE id=? AND status='pending' AND expires_at<=?`, now.UnixMilli(), item.id, now.UnixMilli())
		if updateErr != nil {
			return updateErr
		}
		n, rowsErr := res.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if n != 1 {
			continue
		}
		payload, marshalErr := json.Marshal(map[string]any{"approval_id": item.id, "requester_agent_id": item.requesterID, "status": "expired"})
		if marshalErr != nil {
			return marshalErr
		}
		eventID, idErr := newID()
		if idErr != nil {
			return idErr
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO collaboration_outbox
			(id,workspace_id,aggregate_type,aggregate_id,event_type,payload_json,created_at)
			VALUES(?,?, 'approval', ?, 'approval.expired', ?, ?)`,
			eventID, item.workspaceID, item.id, string(payload), now.UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// recordDispatchFailure applies bounded exponential retry to poison or transient
// dispatch failures. Once the ceiling is reached, the event is dead-lettered so it
// cannot block unrelated events behind it.
func (d *Dispatcher) recordDispatchFailure(ctx context.Context, id string, cause error) error {
	if cause == nil {
		return nil
	}
	message := strings.TrimSpace(cause.Error())
	if len(message) > 2000 {
		message = message[:2000]
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Join(cause, err)
	}
	defer tx.Rollback()
	var attempts int
	err = tx.QueryRowContext(ctx, "SELECT attempts FROM collaboration_outbox WHERE id=? AND published_at IS NULL", id).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Join(cause, err)
	}
	attempts++
	now := time.Now().UTC()
	var published any
	lastError := message
	nextAttempt := now.Add(time.Second * time.Duration(1<<min(attempts-1, 8))).UnixMilli()
	if attempts >= maxOutboxAttempts {
		published = now.UnixMilli()
		lastError = "dead-letter: " + message
		nextAttempt = now.UnixMilli()
	}
	_, err = tx.ExecContext(ctx, "UPDATE collaboration_outbox SET attempts=?,next_attempt_at=?,published_at=?,last_error=? WHERE id=? AND published_at IS NULL",
		attempts, nextAttempt, published, lastError, id)
	if err != nil {
		return errors.Join(cause, err)
	}
	if err = tx.Commit(); err != nil {
		return errors.Join(cause, err)
	}
	return fmt.Errorf("outbox event %s dispatch attempt %d: %w", id, attempts, cause)
}

// routeRecipients is intentionally an allow-list. Adding a new event type requires
// defining and testing its recipient semantics here.
func routeRecipients(ctx context.Context, tx *sql.Tx, event Event) ([]string, error) {
	switch event.Type {
	case "task.assigned", "task.assignment_retried":
		var p struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode task.assigned payload: %w", err)
		}
		if p.AgentID == "" {
			return nil, errors.New("task.assigned event is missing agent_id")
		}
		var count int
		err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_identities WHERE id=? AND workspace_id=? AND status<>'disabled'", p.AgentID, event.WorkspaceID).Scan(&count)
		if err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, fmt.Errorf("task.assigned recipient %q is not an active identity in workspace %q", p.AgentID, event.WorkspaceID)
		}
		return []string{p.AgentID}, nil

	case "task.completed":
		var p struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode task.completed payload: %w", err)
		}
		if p.TaskID == "" || p.TaskID != event.AggregateID {
			return nil, errors.New("task.completed event has an invalid task_id")
		}
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT recipient_id FROM (
			SELECT a.id AS recipient_id
			FROM collaboration_tasks t
			JOIN agent_identities a ON a.id=t.created_by
			WHERE t.id=? AND t.workspace_id=? AND a.status<>'disabled'
			UNION
			SELECT a.id AS recipient_id
			FROM collaboration_tasks child
			JOIN task_assignments parent_assignment ON parent_assignment.task_id=child.parent_task_id
			JOIN agent_identities a ON a.id=parent_assignment.agent_id
			WHERE child.id=? AND child.workspace_id=?
			  AND parent_assignment.status='claimed' AND a.status<>'disabled'
		) ORDER BY recipient_id`, p.TaskID, event.WorkspaceID, p.TaskID, event.WorkspaceID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanRecipients(rows)

	case "message.created":
		var p struct {
			ChannelID string `json:"channel_id"`
		}
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode message.created payload: %w", err)
		}
		if p.ChannelID == "" {
			return nil, errors.New("message.created event is missing channel_id")
		}
		rows, err := tx.QueryContext(ctx, `SELECT m.agent_id
			FROM collaboration_channel_members m
			JOIN collaboration_channels c ON c.id=m.channel_id
			JOIN agent_identities a ON a.id=m.agent_id
			WHERE m.channel_id=? AND c.workspace_id=? AND c.archived_at IS NULL AND a.status<>'disabled'
			ORDER BY m.agent_id`, p.ChannelID, event.WorkspaceID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanRecipients(rows)

	case "approval.requested":
		var p struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode approval.requested payload: %w", err)
		}
		if p.AgentID == "" {
			return nil, errors.New("approval.requested event is missing agent_id")
		}
		return activeWorkspaceRecipient(ctx, tx, event.WorkspaceID, p.AgentID)

	case "approval.approved", "approval.rejected", "approval.expired":
		var p struct {
			ApprovalID string `json:"approval_id"`
		}
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return nil, fmt.Errorf("decode %s payload: %w", event.Type, err)
		}
		if p.ApprovalID == "" {
			return nil, fmt.Errorf("%s event is missing approval_id", event.Type)
		}
		var requester sql.NullString
		err := tx.QueryRowContext(ctx, "SELECT requester_agent_id FROM collaboration_approvals WHERE id=? AND workspace_id=?", p.ApprovalID, event.WorkspaceID).Scan(&requester)
		if errors.Is(err, sql.ErrNoRows) || !requester.Valid || requester.String == "" {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return activeWorkspaceRecipient(ctx, tx, event.WorkspaceID, requester.String)

	default:
		return nil, nil
	}
}

func activeWorkspaceRecipient(ctx context.Context, tx *sql.Tx, workspaceID, agentID string) ([]string, error) {
	var count int
	err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_identities WHERE id=? AND workspace_id=? AND status<>'disabled'", agentID, workspaceID).Scan(&count)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	return []string{agentID}, nil
}

func scanRecipients(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	recipients := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		recipients = append(recipients, id)
	}
	return recipients, rows.Err()
}
