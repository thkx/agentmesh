package collaboration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// TaskResult is the durable result delivered to an Agent after another Agent
// completes an assigned task.
type TaskResult struct {
	ID               string          `json:"id"`
	WorkspaceID      string          `json:"workspace_id"`
	TaskID           string          `json:"task_id"`
	AssignmentID     string          `json:"assignment_id"`
	ExecutionID      string          `json:"execution_id"`
	RecipientAgentID string          `json:"recipient_agent_id"`
	Result           json.RawMessage `json:"result"`
	ReceivedAt       time.Time       `json:"received_at"`
}

// StoreTaskResult consumes a task.completed Inbox event. The unique
// assignment/recipient key makes retries safe when a worker crashes after
// persisting the result but before acknowledging its Inbox lease.
func (s *Service) StoreTaskResult(ctx context.Context, recipientAgentID, taskID, assignmentID, executionID string, result json.RawMessage) error {
	recipientAgentID = strings.TrimSpace(recipientAgentID)
	taskID = strings.TrimSpace(taskID)
	assignmentID = strings.TrimSpace(assignmentID)
	executionID = strings.TrimSpace(executionID)
	if recipientAgentID == "" || taskID == "" || assignmentID == "" || len(executionID) > 128 || len(result) > 4<<20 {
		return ErrInvalid
	}
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	if !json.Valid(result) {
		return ErrInvalid
	}
	var workspaceID, assignedTaskID string
	err := s.db.QueryRowContext(ctx, `SELECT t.workspace_id,a.task_id
        FROM task_assignments a JOIN collaboration_tasks t ON t.id=a.task_id
        WHERE a.id=? AND t.id=? AND a.status='completed'`, assignmentID, taskID).Scan(&workspaceID, &assignedTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if assignedTaskID != taskID {
		return ErrConflict
	}
	var recipientWorkspace string
	err = s.db.QueryRowContext(ctx, "SELECT workspace_id FROM agent_identities WHERE id=? AND status<>'disabled'", recipientAgentID).Scan(&recipientWorkspace)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if recipientWorkspace != workspaceID {
		return ErrConflict
	}
	id, err := newID()
	if err != nil {
		return err
	}
	now := s.clock().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO agent_task_results
        (id,workspace_id,task_id,assignment_id,execution_id,recipient_agent_id,result_json,received_at)
        VALUES(?,?,?,?,?,?,?,?)
        ON CONFLICT(assignment_id,recipient_agent_id) DO NOTHING`,
		id, workspaceID, taskID, assignmentID, executionID, recipientAgentID, string(result), now.UnixMilli())
	if err != nil {
		return err
	}
	var storedTask, storedExecution, storedResult string
	err = s.db.QueryRowContext(ctx, `SELECT task_id,execution_id,result_json FROM agent_task_results
        WHERE assignment_id=? AND recipient_agent_id=?`, assignmentID, recipientAgentID).
		Scan(&storedTask, &storedExecution, &storedResult)
	if err != nil {
		return err
	}
	if storedTask != taskID || storedExecution != executionID || storedResult != string(result) {
		return ErrConflict
	}
	return nil
}

// ListTaskResults returns the newest results delivered to an Agent.
func (s *Service) ListTaskResults(ctx context.Context, recipientAgentID string, limit int) ([]TaskResult, error) {
	recipientAgentID = strings.TrimSpace(recipientAgentID)
	if recipientAgentID == "" {
		return nil, ErrInvalid
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace_id,task_id,assignment_id,execution_id,
        recipient_agent_id,result_json,received_at FROM agent_task_results
        WHERE recipient_agent_id=? ORDER BY received_at DESC,id LIMIT ?`, recipientAgentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TaskResult, 0)
	for rows.Next() {
		var item TaskResult
		var result string
		var received int64
		if err = rows.Scan(&item.ID, &item.WorkspaceID, &item.TaskID, &item.AssignmentID, &item.ExecutionID,
			&item.RecipientAgentID, &result, &received); err != nil {
			return nil, err
		}
		item.Result = json.RawMessage(result)
		item.ReceivedAt = time.UnixMilli(received).UTC()
		out = append(out, item)
	}
	return out, rows.Err()
}
