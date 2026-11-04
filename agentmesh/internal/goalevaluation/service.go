package goalevaluation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	evaldom "github.com/thkx/agentmesh/internal/domain/goalevaluation"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
)

var (
	ErrRunNotTerminal          = errors.New("workflow run is not terminal")
	ErrNoSuccessCriteria       = errors.New("goal has no success criteria")
	ErrInvalidEvaluationResult = errors.New("evaluator result does not match goal criteria or run evidence")
	ErrGoalNotEvaluable        = errors.New("goal is not eligible for evaluation")
)

type GoalReader interface {
	Get(context.Context, string) (goaldom.Goal, error)
}
type WorkflowReader interface {
	GetRun(context.Context, string) (wf.Run, error)
}
type Service struct {
	goals     GoalReader
	runs      WorkflowReader
	repo      Repository
	evaluator Evaluator
}

func NewService(goals GoalReader, runs WorkflowReader, repo Repository, evaluator Evaluator) (*Service, error) {
	if goals == nil || runs == nil || repo == nil || evaluator == nil {
		return nil, errors.New("goal evaluation dependencies are required")
	}
	return &Service{goals: goals, runs: runs, repo: repo, evaluator: evaluator}, nil
}

type EvaluateRequest struct {
	RunID   string `json:"run_id"`
	ModelID string `json:"model_id"`
}

func (s *Service) Evaluate(ctx context.Context, goalID string, req EvaluateRequest) (evaldom.Evaluation, error) {
	goalID, req.RunID, req.ModelID = strings.TrimSpace(goalID), strings.TrimSpace(req.RunID), strings.TrimSpace(req.ModelID)
	if goalID == "" || req.RunID == "" || req.ModelID == "" {
		return evaldom.Evaluation{}, errors.New("goal_id, run_id and model_id are required")
	}
	// Return an existing immutable evaluation before checking lifecycle state. This
	// makes retries idempotent even after the evaluation completed the Goal.
	if existing, err := s.repo.Get(ctx, goalID, req.RunID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return evaldom.Evaluation{}, err
	}
	goal, err := s.goals.Get(ctx, goalID)
	if err != nil {
		return evaldom.Evaluation{}, err
	}
	if goal.Status != goaldom.StatusActive {
		return evaldom.Evaluation{}, ErrGoalNotEvaluable
	}
	if len(goal.SuccessCriteria) == 0 {
		return evaldom.Evaluation{}, ErrNoSuccessCriteria
	}
	if _, err := s.repo.FindExecution(ctx, goalID, req.RunID); err != nil {
		return evaldom.Evaluation{}, err
	}
	run, err := s.runs.GetRun(ctx, req.RunID)
	if err != nil {
		return evaldom.Evaluation{}, err
	}
	if !terminal(run.Status) {
		return evaldom.Evaluation{}, ErrRunNotTerminal
	}
	summary, results, err := s.evaluator.Evaluate(ctx, goal, run, req.ModelID)
	if err != nil {
		return evaldom.Evaluation{}, err
	}
	if err := validateResults(goal, run, results); err != nil {
		return evaldom.Evaluation{}, err
	}
	status := determineStatus(goal, run, results)
	id, err := newEvaluationID()
	if err != nil {
		return evaldom.Evaluation{}, err
	}
	evaluation := evaldom.Evaluation{ID: id, GoalID: goalID, RunID: req.RunID, ModelID: req.ModelID, ExecutionStatus: string(run.Status), Status: status, Summary: summary, Criteria: results, CreatedAt: time.Now().UTC()}
	if err := evaluation.Validate(); err != nil {
		return evaldom.Evaluation{}, fmt.Errorf("invalid evaluation: %w", err)
	}
	return s.repo.Save(ctx, evaluation, status == evaldom.StatusSatisfied && run.Status == wf.RunSucceeded)
}

func (s *Service) Get(ctx context.Context, goalID, runID string) (evaldom.Evaluation, error) {
	return s.repo.Get(ctx, strings.TrimSpace(goalID), strings.TrimSpace(runID))
}
func (s *Service) List(ctx context.Context, goalID string, limit int) ([]evaldom.Evaluation, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	return s.repo.List(ctx, strings.TrimSpace(goalID), limit)
}

func terminal(status wf.RunStatus) bool {
	return status == wf.RunSucceeded || status == wf.RunFailed || status == wf.RunRejected || status == wf.RunCanceled
}

func validateResults(goal goaldom.Goal, run wf.Run, results []evaldom.CriterionResult) error {
	if len(results) != len(goal.SuccessCriteria) {
		return ErrInvalidEvaluationResult
	}
	criteria := make(map[string]goaldom.SuccessCriterion, len(goal.SuccessCriteria))
	for _, c := range goal.SuccessCriteria {
		criteria[c.ID] = c
	}
	outputs := make(map[string]string)
	for id, node := range run.Nodes {
		if node.Status == wf.NodeSucceeded && len(node.Output) > 0 {
			outputs[id] = string(node.Output)
		}
	}
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		if _, ok := criteria[result.CriterionID]; !ok {
			return ErrInvalidEvaluationResult
		}
		if _, ok := seen[result.CriterionID]; ok {
			return ErrInvalidEvaluationResult
		}
		seen[result.CriterionID] = struct{}{}
		if result.Status != evaldom.StatusSatisfied && result.Status != evaldom.StatusNotSatisfied && result.Status != evaldom.StatusInconclusive {
			return ErrInvalidEvaluationResult
		}
		if result.Status == evaldom.StatusSatisfied && len(result.Evidence) == 0 {
			return fmt.Errorf("%w: satisfied criterion %s has no evidence", ErrInvalidEvaluationResult, result.CriterionID)
		}
		for _, evidence := range result.Evidence {
			output, ok := outputs[evidence.NodeID]
			if !ok || strings.TrimSpace(evidence.Quote) == "" || !strings.Contains(output, evidence.Quote) {
				return fmt.Errorf("%w: evidence for criterion %s is not present in successful node %s", ErrInvalidEvaluationResult, result.CriterionID, evidence.NodeID)
			}
		}
	}
	return nil
}

func determineStatus(goal goaldom.Goal, run wf.Run, results []evaldom.CriterionResult) evaldom.Status {
	byID := make(map[string]evaldom.Status, len(results))
	for _, r := range results {
		byID[r.CriterionID] = r.Status
	}
	if run.Status != wf.RunSucceeded {
		return evaldom.StatusInconclusive
	}
	inconclusive := false
	requiredCount := 0
	for _, c := range goal.SuccessCriteria {
		if !c.Required {
			continue
		}
		requiredCount++
		switch byID[c.ID] {
		case evaldom.StatusNotSatisfied:
			return evaldom.StatusNotSatisfied
		case evaldom.StatusInconclusive:
			inconclusive = true
		}
	}
	if requiredCount == 0 || inconclusive {
		return evaldom.StatusInconclusive
	}
	return evaldom.StatusSatisfied
}

func newEvaluationID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
