package reflection

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
	dom "github.com/thkx/agentmesh/internal/domain/reflection"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
)

var (
	ErrInvalidRequest     = errors.New("goal_id, run_id and model_id are required")
	ErrInvalidEvidence    = errors.New("reflection contains evidence not present in workflow execution")
	ErrEvaluationRequired = errors.New("goal evaluation is required before reflection")
)

type GoalReader interface {
	Get(context.Context, string) (goaldom.Goal, error)
}
type RunReader interface {
	GetRun(context.Context, string) (wf.Run, error)
}
type EvaluationReader interface {
	Get(context.Context, string, string) (evaldom.Evaluation, error)
}
type Service struct {
	goals       GoalReader
	runs        RunReader
	evaluations EvaluationReader
	repo        Repository
	reflector   Reflector
}

func NewService(g GoalReader, r RunReader, e EvaluationReader, repo Repository, reflector Reflector) (*Service, error) {
	if g == nil || r == nil || e == nil || repo == nil || reflector == nil {
		return nil, errors.New("reflection dependencies are required")
	}
	return &Service{g, r, e, repo, reflector}, nil
}

type CreateRequest struct {
	RunID   string `json:"run_id"`
	ModelID string `json:"model_id"`
}

func (s *Service) Create(ctx context.Context, goalID string, req CreateRequest) (dom.Reflection, error) {
	goalID = strings.TrimSpace(goalID)
	req.RunID = strings.TrimSpace(req.RunID)
	req.ModelID = strings.TrimSpace(req.ModelID)
	if goalID == "" || req.RunID == "" || req.ModelID == "" {
		return dom.Reflection{}, ErrInvalidRequest
	}
	if old, err := s.repo.Get(ctx, goalID, req.RunID); err == nil {
		return old, nil
	} else if !errors.Is(err, ErrNotFound) {
		return dom.Reflection{}, err
	}
	goal, err := s.goals.Get(ctx, goalID)
	if err != nil {
		return dom.Reflection{}, err
	}
	evaluation, err := s.evaluations.Get(ctx, goalID, req.RunID)
	if err != nil {
		if errors.Is(err, evaldom.ErrNotFound) {
			return dom.Reflection{}, ErrEvaluationRequired
		}
		return dom.Reflection{}, err
	}
	run, err := s.runs.GetRun(ctx, req.RunID)
	if err != nil {
		return dom.Reflection{}, err
	}
	summary, insights, err := s.reflector.Reflect(ctx, goal, run, evaluation, req.ModelID)
	if err != nil {
		return dom.Reflection{}, err
	}
	if err = validateEvidence(run, insights); err != nil {
		return dom.Reflection{}, err
	}
	id, err := newID()
	if err != nil {
		return dom.Reflection{}, err
	}
	item := dom.Reflection{ID: id, GoalID: goalID, RunID: req.RunID, EvaluationID: evaluation.ID, ModelID: req.ModelID, Summary: summary, Insights: insights, CreatedAt: time.Now().UTC()}
	if err = item.Validate(); err != nil {
		return dom.Reflection{}, fmt.Errorf("invalid reflection: %w", err)
	}
	return s.repo.Save(ctx, item)
}
func (s *Service) Get(ctx context.Context, goalID, runID string) (dom.Reflection, error) {
	return s.repo.Get(ctx, strings.TrimSpace(goalID), strings.TrimSpace(runID))
}
func (s *Service) List(ctx context.Context, goalID string, limit int) ([]dom.Reflection, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	return s.repo.List(ctx, strings.TrimSpace(goalID), limit)
}
func validateEvidence(run wf.Run, insights []dom.Insight) error {
	for _, ins := range insights {
		for _, ev := range ins.Evidence {
			n, ok := run.Nodes[ev.NodeID]
			if !ok || strings.TrimSpace(ev.Quote) == "" || (!strings.Contains(string(n.Output), ev.Quote) && !strings.Contains(n.Error, ev.Quote)) {
				return fmt.Errorf("%w: node %s", ErrInvalidEvidence, ev.NodeID)
			}
		}
	}
	return nil
}
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
