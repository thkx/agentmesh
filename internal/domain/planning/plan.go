package planning

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	ErrInvalidPlan = errors.New("invalid plan")
	ErrInvalidStep = errors.New("invalid plan step")
)

type StepType string

const (
	StepAgent     StepType = "agent"
	StepCondition StepType = "condition"
	StepApproval  StepType = "approval"
	StepEnd       StepType = "end"
)

type PlanStatus string

const (
	PlanValidated PlanStatus = "validated"
)

type RetryPolicy struct {
	MaxAttempts int `json:"max_attempts,omitempty"`
}

type Step struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Type         StepType       `json:"type"`
	Dependencies []string       `json:"dependencies,omitempty"`
	AgentProfile string         `json:"agent_profile,omitempty"`
	Input        map[string]any `json:"input,omitempty"`
	RetryPolicy  RetryPolicy    `json:"retry_policy,omitempty"`
}

type Plan struct {
	ID        string     `json:"id"`
	GoalID    string     `json:"goal_id"`
	Version   int        `json:"version"`
	Objective string     `json:"objective"`
	Steps     []Step     `json:"steps"`
	Status    PlanStatus `json:"status"`
	ModelID   string     `json:"model_id"`
	CreatedAt time.Time  `json:"created_at"`
}

var stepIDPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

func (p Plan) Validate() error {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.GoalID) == "" || strings.TrimSpace(p.Objective) == "" || len(p.Objective) > 10000 || len(p.Steps) == 0 || len(p.Steps) > 128 {
		return ErrInvalidPlan
	}
	ids := make(map[string]Step, len(p.Steps))
	for _, step := range p.Steps {
		if !stepIDPattern.MatchString(step.ID) || strings.TrimSpace(step.Name) == "" || strings.TrimSpace(step.Description) == "" || len(step.Name) > 200 || len(step.Description) > 4000 {
			return fmt.Errorf("%w: malformed step", ErrInvalidStep)
		}
		switch step.Type {
		case StepAgent, StepCondition, StepApproval, StepEnd:
		default:
			return fmt.Errorf("%w: unsupported type %q", ErrInvalidStep, step.Type)
		}
		if step.Type == StepAgent && strings.TrimSpace(step.AgentProfile) == "" {
			return fmt.Errorf("%w: agent_profile is required for agent step %q", ErrInvalidStep, step.ID)
		}
		if step.RetryPolicy.MaxAttempts < 0 || step.RetryPolicy.MaxAttempts > 10 {
			return fmt.Errorf("%w: retry max_attempts must be between 0 and 10", ErrInvalidStep)
		}
		if _, exists := ids[step.ID]; exists {
			return fmt.Errorf("%w: duplicate step id %q", ErrInvalidStep, step.ID)
		}
		ids[step.ID] = step
	}
	indegree := make(map[string]int, len(ids))
	adj := make(map[string][]string, len(ids))
	for _, step := range p.Steps {
		seenDeps := make(map[string]struct{}, len(step.Dependencies))
		for _, dep := range step.Dependencies {
			if dep == step.ID {
				return fmt.Errorf("%w: step %q depends on itself", ErrInvalidStep, step.ID)
			}
			if _, ok := ids[dep]; !ok {
				return fmt.Errorf("%w: step %q references unknown dependency %q", ErrInvalidStep, step.ID, dep)
			}
			if _, ok := seenDeps[dep]; ok {
				return fmt.Errorf("%w: duplicate dependency %q", ErrInvalidStep, dep)
			}
			seenDeps[dep] = struct{}{}
			indegree[step.ID]++
			adj[dep] = append(adj[dep], step.ID)
		}
	}
	queue := make([]string, 0, len(ids))
	for id := range ids {
		if indegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range adj[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(ids) {
		return fmt.Errorf("%w: dependency graph contains a cycle", ErrInvalidPlan)
	}
	return nil
}
