package goalevaluation

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidEvaluation = errors.New("invalid goal evaluation")
	ErrNotFound          = errors.New("goal evaluation not found")
	ErrExecutionNotFound = errors.New("workflow run is not linked to this goal")
	ErrGoalNotActive     = errors.New("goal is not active")
)

type ExecutionLink struct {
	GoalID      string
	PlanID      string
	PlanVersion int
	RunID       string
}

type Status string

const (
	StatusSatisfied    Status = "satisfied"
	StatusNotSatisfied Status = "not_satisfied"
	StatusInconclusive Status = "inconclusive"
)

type Evidence struct {
	NodeID string `json:"node_id"`
	Quote  string `json:"quote"`
}

type CriterionResult struct {
	CriterionID string     `json:"criterion_id"`
	Status      Status     `json:"status"`
	Reason      string     `json:"reason"`
	Evidence    []Evidence `json:"evidence"`
}

type Evaluation struct {
	ID              string            `json:"id"`
	GoalID          string            `json:"goal_id"`
	RunID           string            `json:"run_id"`
	ModelID         string            `json:"model_id"`
	ExecutionStatus string            `json:"execution_status"`
	Status          Status            `json:"status"`
	Summary         string            `json:"summary"`
	Criteria        []CriterionResult `json:"criteria"`
	CreatedAt       time.Time         `json:"created_at"`
}

func (e Evaluation) Validate() error {
	if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.GoalID) == "" || strings.TrimSpace(e.RunID) == "" || strings.TrimSpace(e.ModelID) == "" || strings.TrimSpace(e.Summary) == "" || len(e.Summary) > 8000 || len(e.Criteria) == 0 || len(e.Criteria) > 64 {
		return ErrInvalidEvaluation
	}
	if e.Status != StatusSatisfied && e.Status != StatusNotSatisfied && e.Status != StatusInconclusive {
		return ErrInvalidEvaluation
	}
	seen := make(map[string]struct{}, len(e.Criteria))
	for _, result := range e.Criteria {
		if strings.TrimSpace(result.CriterionID) == "" || strings.TrimSpace(result.Reason) == "" || len(result.Reason) > 4000 {
			return ErrInvalidEvaluation
		}
		if result.Status != StatusSatisfied && result.Status != StatusNotSatisfied && result.Status != StatusInconclusive {
			return ErrInvalidEvaluation
		}
		if _, ok := seen[result.CriterionID]; ok {
			return ErrInvalidEvaluation
		}
		seen[result.CriterionID] = struct{}{}
		if len(result.Evidence) > 16 {
			return ErrInvalidEvaluation
		}
		for _, evidence := range result.Evidence {
			if strings.TrimSpace(evidence.NodeID) == "" || strings.TrimSpace(evidence.Quote) == "" || len(evidence.Quote) > 4000 {
				return ErrInvalidEvaluation
			}
		}
	}
	return nil
}
