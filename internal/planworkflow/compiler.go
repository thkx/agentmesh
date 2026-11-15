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

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
	wfcompiler "github.com/thkx/agentmesh/internal/workflow/compiler"
)

var (
	ErrInvalidProfileBinding = errors.New("invalid agent profile binding")
	ErrIdempotencyConflict   = errors.New("plan version already started with different input or profile bindings")
)

type AgentProfile struct {
	ModelID     string `json:"model_id"`
	WorkspaceID string `json:"workspace_id"`
}

type ProfileValidator interface {
	ValidateProfile(context.Context, string, AgentProfile) error
}

type CompileOptions struct {
	Profiles  map[string]AgentProfile
	Validator ProfileValidator
}

// Compile converts an immutable M12 Plan snapshot into an immutable M8 Workflow DAG.
func Compile(ctx context.Context, goal goaldom.Goal, plan pland.Plan, opts CompileOptions) (wf.Definition, error) {
	if err := plan.Validate(); err != nil {
		return wf.Definition{}, fmt.Errorf("validate plan: %w", err)
	}
	if plan.Status != pland.PlanValidated {
		return wf.Definition{}, fmt.Errorf("plan status %q is not executable", plan.Status)
	}
	if goal.ID != plan.GoalID || goal.Status != goaldom.StatusActive {
		return wf.Definition{}, errors.New("plan goal is missing, mismatched, or not active")
	}
	byID := make(map[string]pland.Step, len(plan.Steps))
	for _, step := range plan.Steps {
		byID[step.ID] = step
	}

	definitionID := stableID("plan", plan.GoalID, plan.ID, fmt.Sprint(plan.Version))
	definition := wf.Definition{ID: definitionID, Name: fmt.Sprintf("Goal %s Plan v%d", goal.ID, plan.Version), Version: 1, CreatedAt: plan.CreatedAt}
	rootIDs := make([]string, 0)
	for _, step := range plan.Steps {
		if len(step.Dependencies) == 0 {
			rootIDs = append(rootIDs, step.ID)
		}
	}
	sort.Strings(rootIDs)
	if len(rootIDs) == 0 {
		return wf.Definition{}, errors.New("plan has no entry step")
	}

	for _, step := range plan.Steps {
		node := wf.Node{ID: stepNodeID(step.ID), Name: step.Name}
		switch step.Type {
		case pland.StepAgent:
			binding, ok := opts.Profiles[step.AgentProfile]
			if !ok || strings.TrimSpace(binding.ModelID) == "" || strings.TrimSpace(binding.WorkspaceID) == "" {
				return wf.Definition{}, fmt.Errorf("%w: profile %q is not bound to model_id and workspace_id", ErrInvalidProfileBinding, step.AgentProfile)
			}
			if opts.Validator != nil {
				if err := opts.Validator.ValidateProfile(ctx, step.AgentProfile, binding); err != nil {
					return wf.Definition{}, fmt.Errorf("%w: %v", ErrInvalidProfileBinding, err)
				}
			}
			inputJSON, err := json.Marshal(step.Input)
			if err != nil {
				return wf.Definition{}, fmt.Errorf("encode step %s input: %w", step.ID, err)
			}
			var prompt strings.Builder
			fmt.Fprintf(&prompt, "Goal: %s\nPlan objective: %s\nStep: %s\n", goal.Description, plan.Objective, step.Description)
			if len(goal.Constraints) > 0 {
				prompt.WriteString("Constraints:\n")
				for _, constraint := range goal.Constraints {
					fmt.Fprintf(&prompt, "- %s\n", constraint)
				}
			}
			if len(goal.SuccessCriteria) > 0 {
				prompt.WriteString("Success criteria:\n")
				for _, criterion := range goal.SuccessCriteria {
					fmt.Fprintf(&prompt, "- %s: %s (required=%t)\n", criterion.ID, criterion.Description, criterion.Required)
				}
			}
			if len(goal.Context) > 0 {
				contextJSON, marshalErr := json.Marshal(goal.Context)
				if marshalErr != nil {
					return wf.Definition{}, fmt.Errorf("encode goal context: %w", marshalErr)
				}
				fmt.Fprintf(&prompt, "Goal context (JSON):\n%s\n", string(contextJSON))
			}
			if len(step.Input) > 0 {
				fmt.Fprintf(&prompt, "\nStructured step input (JSON):\n%s", string(inputJSON))
			}
			node.Type = wf.NodeAgent
			node.Agent = &wf.AgentConfig{AgentProfile: step.AgentProfile, ModelID: binding.ModelID, WorkspaceID: binding.WorkspaceID, Prompt: prompt.String()}
		case pland.StepCondition:
			inputKey, okKey := step.Input["input_key"].(string)
			trueNext, okTrue := step.Input["true_next"].(string)
			falseNext, okFalse := step.Input["false_next"].(string)
			equals, hasEquals := step.Input["equals"]
			if !okKey || strings.TrimSpace(inputKey) == "" || !okTrue || !okFalse || !hasEquals {
				return wf.Definition{}, fmt.Errorf("condition step %q requires input.input_key, input.equals, input.true_next and input.false_next", step.ID)
			}
			if _, ok := byID[trueNext]; !ok {
				return wf.Definition{}, fmt.Errorf("condition step %q true_next references unknown step %q", step.ID, trueNext)
			}
			if _, ok := byID[falseNext]; !ok {
				return wf.Definition{}, fmt.Errorf("condition step %q false_next references unknown step %q", step.ID, falseNext)
			}
			if !contains(byID[trueNext].Dependencies, step.ID) || !contains(byID[falseNext].Dependencies, step.ID) {
				return wf.Definition{}, fmt.Errorf("condition step %q branch targets must declare the condition as a dependency", step.ID)
			}
			node.Type = wf.NodeCondition
			node.Condition = &wf.ConditionConfig{InputKey: inputKey, Equals: equals}
			definition.Edges = append(definition.Edges,
				wf.Edge{From: node.ID, To: stepNodeID(trueNext), Handle: "true"},
				wf.Edge{From: node.ID, To: stepNodeID(falseNext), Handle: "false"},
			)
		case pland.StepApproval:
			node.Type = wf.NodeApproval
			node.Approval = &wf.ApprovalConfig{Message: step.Description}
		case pland.StepEnd:
			node.Type = wf.NodeEnd
		default:
			return wf.Definition{}, fmt.Errorf("unsupported plan step type %q", step.Type)
		}
		definition.Nodes = append(definition.Nodes, node)
		for _, dep := range step.Dependencies {
			parent := byID[dep]
			if parent.Type == pland.StepCondition {
				// Condition successors are explicit true_next/false_next edges.
				trueNext, _ := parent.Input["true_next"].(string)
				falseNext, _ := parent.Input["false_next"].(string)
				if step.ID != trueNext && step.ID != falseNext {
					return wf.Definition{}, fmt.Errorf("step %q depends on condition %q but is not one of its branches", step.ID, dep)
				}
				continue
			}
			definition.Edges = append(definition.Edges, wf.Edge{From: stepNodeID(dep), To: node.ID})
		}
	}

	if len(rootIDs) == 1 {
		definition.Entry = stepNodeID(rootIDs[0])
	} else {
		// M13 adds a pure no-op start node so a valid Plan may have multiple roots.
		// The existing Workflow Runtime executes all outgoing edges in parallel.
		startID := "__plan_start"
		definition.Nodes = append(definition.Nodes, wf.Node{ID: startID, Type: wf.NodeStart, Name: "Plan entry"})
		for _, root := range rootIDs {
			definition.Edges = append(definition.Edges, wf.Edge{From: startID, To: stepNodeID(root)})
		}
		definition.Entry = startID
	}

	// The existing M8 compiler is the final authority for reachability, DAG shape,
	// terminal nodes, condition branches and edge consistency.
	if _, err := wfcompiler.Compile(definition); err != nil {
		return wf.Definition{}, fmt.Errorf("compiled workflow is invalid: %w", err)
	}
	return definition, nil
}

func stepNodeID(id string) string { return "step-" + id }

func stableID(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "-" + hex.EncodeToString(h[:16])
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
