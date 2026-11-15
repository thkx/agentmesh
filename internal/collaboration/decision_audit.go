package collaboration

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// DecisionAudit is the immutable record of one Workspace policy decision.
type DecisionAudit struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	AgentID      string    `json:"agent_id"`
	EventID      string    `json:"event_id,omitempty"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type,omitempty"`
	ResourceID   string    `json:"resource_id,omitempty"`
	Outcome      string    `json:"outcome"`
	ApprovalID   string    `json:"approval_id,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// ListDecisionAudit returns the newest policy decisions for one Agent Identity.
func (s *Service) ListDecisionAudit(ctx context.Context, agentID string, limit int) ([]DecisionAudit, error) {
	if _, err := s.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace_id,agent_id,event_id,action,resource_type,resource_id,outcome,COALESCE(approval_id,''),reason,created_at
		FROM collaboration_decision_audit WHERE agent_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]DecisionAudit, 0)
	for rows.Next() {
		var item DecisionAudit
		var eventID sql.NullString
		var created int64
		if err = rows.Scan(&item.ID, &item.WorkspaceID, &item.AgentID, &eventID, &item.Action,
			&item.ResourceType, &item.ResourceID, &item.Outcome, &item.ApprovalID, &item.Reason, &created); err != nil {
			return nil, err
		}
		item.EventID = eventID.String
		item.CreatedAt = time.UnixMilli(created).UTC()
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return items, nil
}
