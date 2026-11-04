package planworkflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

type MemoryRetriever interface {
	Retrieve(context.Context, string, string, []string, int) (string, error)
}

type Service struct {
	goals       GoalReader
	plans       PlanReader
	repo        Repository
	engine      WorkflowEngine
	validator   ProfileValidator
	maxAttempts int
	memory      MemoryRetriever
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

func (s *Service) SetMemoryRetriever(retriever MemoryRetriever) { s.memory = retriever }

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

	input := make(map[string]any, len(req.Input)+2)
	for k, v := range req.Input {
		if k == "_agentmesh_memory_context" || k == "_agentmesh_memory_by_profile" {
			return Execution{}, fmt.Errorf("input key %s is reserved", k)
		}
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
	// A previously persisted run owns its original memory snapshot. Return it
	// before retrieving again so later memory promotion/archive cannot mutate an
	// immutable run input or break M13 idempotency.
	maxAttempts := s.retryBudget(plan)
	if reader, ok := s.engine.(interface {
		GetRun(context.Context, string) (wf.Run, error)
	}); ok {
		if existing, readErr := reader.GetRun(ctx, reserved.RunID); readErr == nil {
			// Re-submit the persisted input to repair a possible crash between
			// CreateRun and EnqueueRun. StartQueued is idempotent for that snapshot.
			var existingInput map[string]any
			if len(existing.Input) > 0 {
				if decodeErr := json.Unmarshal(existing.Input, &existingInput); decodeErr != nil {
					return Execution{}, fmt.Errorf("decode existing workflow input: %w", decodeErr)
				}
			}
			queued, queueErr := s.engine.StartQueued(ctx, reserved.WorkflowID, reserved.RunID, existingInput, maxAttempts)
			if queueErr != nil {
				return Execution{}, fmt.Errorf("recover existing plan execution queue: %w", queueErr)
			}
			return Execution{ExecutionRecord: reserved, Run: queued}, nil
		} else if !errors.Is(readErr, wf.ErrNotFound) {
			return Execution{}, fmt.Errorf("read existing plan run: %w", readErr)
		}
	}
	if err = s.engine.PutDefinition(ctx, definition); err != nil {
		return Execution{}, fmt.Errorf("persist compiled workflow: %w", err)
	}
	if s.memory != nil {
		profileQueries := make(map[string][]string)
		for _, step := range plan.Steps {
			if step.Type == pland.StepAgent && step.AgentProfile != "" {
				if _, ok := profileQueries[step.AgentProfile]; !ok {
					profileQueries[step.AgentProfile] = []string{goal.Description, plan.Objective}
				}
				profileQueries[step.AgentProfile] = append(profileQueries[step.AgentProfile], step.Description)
			}
		}
		profiles := make([]string, 0, len(profileQueries))
		for profile := range profileQueries {
			profiles = append(profiles, profile)
		}
		sort.Strings(profiles)
		byProfile := make(map[string]any)
		for _, profile := range profiles {
			memoryContext, memoryErr := s.memory.Retrieve(ctx, strings.Join(profileQueries[profile], "\n"), goalID, []string{profile}, 8)
			if memoryErr != nil {
				return Execution{}, fmt.Errorf("retrieve memories for agent profile %q: %w", profile, memoryErr)
			}
			if memoryContext != "" {
				byProfile[profile] = memoryContext
			}
		}
		if len(byProfile) > 0 {
			input["_agentmesh_memory_by_profile"] = byProfile
		}
	}
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
