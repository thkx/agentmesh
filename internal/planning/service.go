package planning

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
)

var ErrGoalCanceled = errors.New("cannot plan a canceled goal")

type GoalReader interface {
	Get(context.Context, string) (goaldom.Goal, error)
}

type Service struct {
	goals   GoalReader
	planner Planner
	plans   Repository
}

func NewService(goals GoalReader, planner Planner, plans Repository) (*Service, error) {
	if goals == nil || planner == nil || plans == nil {
		return nil, errors.New("planning service dependencies are required")
	}
	return &Service{goals: goals, planner: planner, plans: plans}, nil
}

func (s *Service) Generate(ctx context.Context, goalID, modelID string) (pland.Plan, error) {
	goalID, modelID = strings.TrimSpace(goalID), strings.TrimSpace(modelID)
	if goalID == "" || modelID == "" {
		return pland.Plan{}, errors.New("goal_id and model_id are required")
	}
	g, err := s.goals.Get(ctx, goalID)
	if err != nil {
		return pland.Plan{}, err
	}
	if g.Status != goaldom.StatusActive {
		return pland.Plan{}, ErrGoalCanceled
	}
	draft, err := s.planner.Generate(ctx, g, modelID)
	if err != nil {
		return pland.Plan{}, err
	}
	id, err := newPlanID()
	if err != nil {
		return pland.Plan{}, fmt.Errorf("generate plan id: %w", err)
	}
	plan := pland.Plan{ID: id, GoalID: g.ID, Objective: strings.TrimSpace(draft.Objective), Steps: draft.Steps, Status: pland.PlanValidated, ModelID: modelID, CreatedAt: time.Now().UTC()}
	if err := plan.Validate(); err != nil {
		return pland.Plan{}, fmt.Errorf("planner produced an invalid plan: %w", err)
	}
	return s.plans.Create(ctx, plan)
}

func (s *Service) Get(ctx context.Context, goalID string, version int) (pland.Plan, error) {
	return s.plans.Get(ctx, strings.TrimSpace(goalID), version)
}

func (s *Service) List(ctx context.Context, goalID string, limit int) ([]pland.Plan, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.plans.List(ctx, strings.TrimSpace(goalID), limit)
}

func newPlanID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
