package collaboration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Dispatcher delivers only events with an explicit, validated recipient rule.
// Unknown event types are marked unroutable; they are never broadcast to a workspace.
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
		if err := d.DispatchOnce(ctx, 100); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *Dispatcher) DispatchOnce(ctx context.Context, limit int) error {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := d.db.QueryContext(ctx, "SELECT id FROM collaboration_outbox WHERE published_at IS NULL ORDER BY created_at,id LIMIT ?", limit)
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
	for _, id := range ids {
		if err = d.dispatchOne(ctx, id); err != nil {
			return err
		}
	}
	return nil
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
		return fmt.Errorf("outbox event %s has invalid JSON payload", id)
	}
	event.Payload = json.RawMessage(payload)
	event.CreatedAt = time.UnixMilli(created).UTC()

	recipients, routeErr := routeRecipients(ctx, tx, event)
	if routeErr != nil {
		return routeErr
	}
	now := time.Now().UTC().UnixMilli()
	for _, agentID := range recipients {
		_, err = tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO collaboration_inbox(id,agent_id,event_id,event_type,payload_json,status,received_at) VALUES(?,?,?,?,?,'pending',?)",
			event.ID+":"+agentID, agentID, event.ID, event.Type, payload, now)
		if err != nil {
			return err
		}
	}
	lastError := ""
	if len(recipients) == 0 {
		lastError = "unroutable: no explicit recipient rule or eligible recipient"
	}
	_, err = tx.ExecContext(ctx,
		"UPDATE collaboration_outbox SET published_at=?,attempts=attempts+1,last_error=? WHERE id=? AND published_at IS NULL",
		now, lastError, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// routeRecipients is intentionally an allow-list. Adding a new event type requires
// defining and testing its recipient semantics here.
func routeRecipients(ctx context.Context, tx *sql.Tx, event Event) ([]string, error) {
	switch event.Type {
	case "task.assigned":
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

	case "approval.approved", "approval.rejected":
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
