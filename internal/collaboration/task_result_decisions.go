package collaboration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// TaskResultAssessment is a durable decision about a completed child execution.
// It deliberately remains separate from the Runtime lifecycle status.
type TaskResultAssessment struct {
	ID               string          `json:"id"`
	ResultID         string          `json:"result_id"`
	WorkspaceID      string          `json:"workspace_id"`
	TaskID           string          `json:"task_id"`
	AssignmentID     string          `json:"assignment_id"`
	RecipientAgentID string          `json:"recipient_agent_id"`
	Outcome          string          `json:"outcome"`
	Reason           string          `json:"reason"`
	Confidence       float64         `json:"confidence"`
	Evidence         json.RawMessage `json:"evidence"`
	CreatedAt        time.Time       `json:"created_at"`
	ReviewerID       string          `json:"reviewer_id,omitempty"`
	DecisionNote     string          `json:"decision_note,omitempty"`
	ReviewedAt       *time.Time      `json:"reviewed_at,omitempty"`
}

// TaskResultEvaluator is the seam for deterministic policy or model-backed review.
// Implementations must treat Task/Result content as untrusted input.
type TaskResultEvaluator interface {
	EvaluateTaskResult(context.Context, Task, TaskResult) (TaskResultAssessment, error)
}

// ConservativeTaskResultEvaluator never equates process success with goal success.
type ConservativeTaskResultEvaluator struct{}

func (ConservativeTaskResultEvaluator) EvaluateTaskResult(_ context.Context, _ Task, _ TaskResult) (TaskResultAssessment, error) {
	return TaskResultAssessment{
		Outcome:    "human_review",
		Reason:     "execution completed, but no configured evaluator can verify task acceptance criteria",
		Confidence: 0,
		Evidence:   json.RawMessage("[]"),
	}, nil
}

func (w *WakeupWorker) processTaskResult(ctx context.Context, recipientID, taskID, assignmentID, executionID string, result json.RawMessage) error {
	if err := w.service.StoreTaskResult(ctx, recipientID, taskID, assignmentID, executionID, result); err != nil {
		return err
	}
	stored, err := w.service.GetTaskResult(ctx, recipientID, assignmentID)
	if err != nil {
		return err
	}
	if _, err = w.service.GetTaskResultAssessment(ctx, recipientID, assignmentID); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	task, err := w.service.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	evaluator := w.resultEvaluator
	if evaluator == nil {
		evaluator = ConservativeTaskResultEvaluator{}
	}
	var assessment TaskResultAssessment
	if contextual, ok := evaluator.(ContextualTaskResultEvaluator); ok {
		evaluationContext, contextErr := w.service.BuildTaskResultEvaluationContext(ctx, recipientID, assignmentID)
		if contextErr != nil {
			return contextErr
		}
		assessment, err = contextual.EvaluateTaskResultWithContext(ctx, evaluationContext)
	} else {
		assessment, err = evaluator.EvaluateTaskResult(ctx, task, stored)
	}
	if err != nil {
		return err
	}
	assessment.ResultID = stored.ID
	assessment.WorkspaceID = stored.WorkspaceID
	assessment.TaskID = stored.TaskID
	assessment.AssignmentID = stored.AssignmentID
	assessment.RecipientAgentID = recipientID
	return w.service.StoreTaskResultAssessment(ctx, assessment)
}

// GetTaskResult retrieves one result delivered to a particular Agent.
func (s *Service) GetTaskResult(ctx context.Context, recipientAgentID, assignmentID string) (TaskResult, error) {
	var item TaskResult
	var result string
	var received int64
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,task_id,assignment_id,execution_id,recipient_agent_id,result_json,received_at
		FROM agent_task_results WHERE recipient_agent_id=? AND assignment_id=?`, recipientAgentID, assignmentID).
		Scan(&item.ID, &item.WorkspaceID, &item.TaskID, &item.AssignmentID, &item.ExecutionID, &item.RecipientAgentID, &result, &received)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	item.Result = json.RawMessage(result)
	item.ReceivedAt = time.UnixMilli(received).UTC()
	return item, nil
}

// StoreTaskResultAssessment is idempotent for a result/recipient pair. A retry
// cannot silently replace an earlier decision with a different assessment.
func (s *Service) StoreTaskResultAssessment(ctx context.Context, item TaskResultAssessment) error {
	item.ResultID = strings.TrimSpace(item.ResultID)
	item.RecipientAgentID = strings.TrimSpace(item.RecipientAgentID)
	item.Outcome = strings.TrimSpace(item.Outcome)
	item.Reason = strings.TrimSpace(item.Reason)
	if item.ResultID == "" || item.RecipientAgentID == "" || len(item.Reason) > 4000 ||
		(item.Outcome != "accepted" && item.Outcome != "rejected" && item.Outcome != "human_review") ||
		item.Confidence < 0 || item.Confidence > 1 || len(item.Evidence) > 64<<10 {
		return ErrInvalid
	}
	if len(item.Evidence) == 0 {
		item.Evidence = json.RawMessage("[]")
	}
	if !json.Valid(item.Evidence) {
		return ErrInvalid
	}
	var workspaceID, taskID, assignmentID string
	err := s.db.QueryRowContext(ctx, `SELECT workspace_id,task_id,assignment_id FROM agent_task_results
		WHERE id=? AND recipient_agent_id=?`, item.ResultID, item.RecipientAgentID).
		Scan(&workspaceID, &taskID, &assignmentID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if (item.WorkspaceID != "" && item.WorkspaceID != workspaceID) ||
		(item.TaskID != "" && item.TaskID != taskID) ||
		(item.AssignmentID != "" && item.AssignmentID != assignmentID) {
		return ErrConflict
	}
	item.WorkspaceID, item.TaskID, item.AssignmentID = workspaceID, taskID, assignmentID
	if item.ID == "" {
		item.ID, err = newID()
		if err != nil {
			return err
		}
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = s.clock().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	inserted, err := tx.ExecContext(ctx, `INSERT INTO agent_task_result_assessments
		(id,result_id,workspace_id,task_id,assignment_id,recipient_agent_id,outcome,reason,confidence,evidence_json,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(result_id,recipient_agent_id) DO NOTHING`,
		item.ID, item.ResultID, item.WorkspaceID, item.TaskID, item.AssignmentID, item.RecipientAgentID,
		item.Outcome, item.Reason, item.Confidence, string(item.Evidence), item.CreatedAt.UTC().UnixMilli())
	if err != nil {
		return err
	}
	insertedCount, err := inserted.RowsAffected()
	if err != nil {
		return err
	}
	var storedOutcome, storedReason, storedEvidence string
	var storedConfidence float64
	err = tx.QueryRowContext(ctx, `SELECT outcome,reason,confidence,evidence_json
		FROM agent_task_result_assessments WHERE result_id=? AND recipient_agent_id=?`,
		item.ResultID, item.RecipientAgentID).Scan(&storedOutcome, &storedReason, &storedConfidence, &storedEvidence)
	if err != nil {
		return err
	}
	if storedOutcome != item.Outcome || storedReason != item.Reason ||
		storedConfidence != item.Confidence || storedEvidence != string(item.Evidence) {
		return ErrConflict
	}
	// A grounded rejection blocks the Task so it can be reassigned to another
	// Agent. The immutable result and assessment remain attached to the old attempt.
	if insertedCount == 1 && item.Outcome == "rejected" {
		now := s.clock().UTC()
		if _, err = tx.ExecContext(ctx, `UPDATE collaboration_tasks SET status='blocked',updated_at=?
			WHERE id=? AND status NOT IN ('canceled','blocked')`, now.UnixMilli(), item.TaskID); err != nil {
			return err
		}
		if err = s.writeEventTx(ctx, tx, item.WorkspaceID, "task", item.TaskID, "task.result_rejected",
			map[string]any{"task_id": item.TaskID, "assignment_id": item.AssignmentID,
				"recipient_agent_id": item.RecipientAgentID, "reason": item.Reason}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetTaskResultAssessment returns the persisted assessment for an Agent's result.
func (s *Service) GetTaskResultAssessment(ctx context.Context, recipientAgentID, assignmentID string) (TaskResultAssessment, error) {
	var item TaskResultAssessment
	var evidence string
	var created int64
	var reviewed sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id,result_id,workspace_id,task_id,assignment_id,recipient_agent_id,outcome,reason,confidence,evidence_json,created_at,
		COALESCE(reviewer_id,''),COALESCE(decision_note,''),reviewed_at
		FROM agent_task_result_assessments WHERE recipient_agent_id=? AND assignment_id=?`, recipientAgentID, assignmentID).
		Scan(&item.ID, &item.ResultID, &item.WorkspaceID, &item.TaskID, &item.AssignmentID, &item.RecipientAgentID,
			&item.Outcome, &item.Reason, &item.Confidence, &evidence, &created, &item.ReviewerID, &item.DecisionNote, &reviewed)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	item.Evidence = json.RawMessage(evidence)
	item.CreatedAt = time.UnixMilli(created).UTC()
	if reviewed.Valid {
		t := time.UnixMilli(reviewed.Int64).UTC()
		item.ReviewedAt = &t
	}
	return item, nil
}
