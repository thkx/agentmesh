package planworkflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
)

type GoalReader interface {
	Get(context.Context, string) (goaldom.Goal, error)
}
type PlanReader interface {
	Get(context.Context, string, int) (pland.Plan, error)
}
type WorkflowEngine interface {
	PutDefinition(context.Context, wf.Definition) error
	StartQueued(context.Context, string, string, map[string]any, int) (wf.Run, error)
}

type Service struct {
	goals       GoalReader
	plans       PlanReader
	repo        Repository
	engine      WorkflowEngine
	validator   ProfileValidator
	maxAttempts int
}

func NewService(goals GoalReader, plans PlanReader, repo Repository, engine WorkflowEngine, validator ProfileValidator, maxAttempts int) (*Service, error) {
	if goals == nil || plans == nil || repo == nil || engine == nil {
		return nil, errors.New("plan execution service dependencies are required")
	}
	if maxAttempts < 1 || maxAttempts > 20 {
		maxAttempts = 3
	}
	return &Service{goals: goals, plans: plans, repo: repo, engine: engine, validator: validator, maxAttempts: maxAttempts}, nil
}

type StartRequest struct {
	Input    map[string]any          `json:"input"`
	Profiles map[string]AgentProfile `json:"agent_profiles"`
}

type Execution struct {
	ExecutionRecord
	Run wf.Run `json:"run"`
}

func (s *Service) Start(ctx context.Context, goalID string, version int, req StartRequest) (Execution, error) {
	goalID = strings.TrimSpace(goalID)
	if goalID == "" || version < 1 {
		return Execution{}, errors.New("goal_id and positive plan version are required")
	}
	goal, err := s.goals.Get(ctx, goalID)
	if err != nil {
		return Execution{}, err
	}
	if goal.Status != goaldom.StatusActive {
		return Execution{}, ErrGoalInactive
	}
	plan, err := s.plans.Get(ctx, goalID, version)
	if err != nil {
		return Execution{}, err
	}
	definition, err := Compile(ctx, goal, plan, CompileOptions{Profiles: req.Profiles, Validator: s.validator})
	if err != nil {
		return Execution{}, err
	}

	input := make(map[string]any, len(req.Input)+1)
	for k, v := range req.Input {
		input[k] = v
	}
	canonical, err := json.Marshal(struct {
		Input    map[string]any          `json:"input"`
		Profiles map[string]AgentProfile `json:"profiles"`
	}{input, req.Profiles})
	if err != nil {
		return Execution{}, fmt.Errorf("encode execution input: %w", err)
	}
	hash := sha256.Sum256(canonical)
	inputHash := hex.EncodeToString(hash[:])
	workflowID := definition.ID
	runID := stableID("run", plan.GoalID, plan.ID, fmt.Sprint(plan.Version))
	record := ExecutionRecord{GoalID: goalID, PlanID: plan.ID, PlanVersion: plan.Version, WorkflowID: workflowID, WorkflowVersion: definition.Version, RunID: runID, InputHash: inputHash, CreatedAt: time.Now().UTC()}
	reserved, err := s.repo.Reserve(ctx, record)
	if err != nil {
		return Execution{}, err
	}
	if err = s.engine.PutDefinition(ctx, definition); err != nil {
		return Execution{}, fmt.Errorf("persist compiled workflow: %w", err)
	}
	maxAttempts := s.retryBudget(plan)
	run, err := s.engine.StartQueued(ctx, reserved.WorkflowID, reserved.RunID, input, maxAttempts)
	if err != nil {
		return Execution{}, fmt.Errorf("enqueue plan execution: %w", err)
	}
	return Execution{ExecutionRecord: reserved, Run: run}, nil
}

// The current Workflow Runtime only exposes a queue lease-expiry recovery ceiling.
// Map the largest declared step budget to that ceiling; this does not retry failed Agent nodes.
func (s *Service) retryBudget(plan pland.Plan) int {
	budget := 0
	for _, step := range plan.Steps {
		if step.RetryPolicy.MaxAttempts > budget {
			budget = step.RetryPolicy.MaxAttempts
		}
	}
	if budget == 0 {
		return s.maxAttempts
	}
	if budget > 20 {
		return 20
	}
	return budget
}
