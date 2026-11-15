package database

import (
	"context"
	"testing"
	"time"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	evaldom "github.com/thkx/agentmesh/internal/domain/goalevaluation"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
)

func TestGoalEvaluationCompletesGoalAtomicallyAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := newGoalTestDB(t)
	goals, _ := NewGoalRepository(db)
	plans, _ := NewPlanRepository(db)
	evaluations, _ := NewGoalEvaluationRepository(db)
	now := time.Now().UTC()
	goal := goaldom.Goal{ID: "eval-goal", Description: "ship feature", Status: goaldom.StatusActive, SuccessCriteria: []goaldom.SuccessCriterion{{ID: "tests", Description: "Tests pass", Required: true}}, CreatedAt: now, UpdatedAt: now}
	if err := goals.Create(ctx, goal); err != nil {
		t.Fatal(err)
	}
	plan, err := plans.Create(ctx, pland.Plan{ID: "eval-plan", GoalID: goal.ID, Objective: "implement", Steps: []pland.Step{{ID: "end", Name: "End", Description: "End", Type: pland.StepEnd}}, Status: pland.PlanValidated, ModelID: "model", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO plan_executions(goal_id,plan_id,plan_version,workflow_id,workflow_version,run_id,input_hash,created_at) VALUES(?,?,?,?,?,?,?,?)`, goal.ID, plan.ID, plan.Version, "wf", 1, "run-eval", "hash", now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := evaluations.FindExecution(ctx, goal.ID, "run-eval"); err != nil {
		t.Fatal(err)
	}
	item := evaldom.Evaluation{ID: "eval-1", GoalID: goal.ID, RunID: "run-eval", ModelID: "model", ExecutionStatus: "succeeded", Status: evaldom.StatusSatisfied, Summary: "All required criteria passed", Criteria: []evaldom.CriterionResult{{CriterionID: "tests", Status: evaldom.StatusSatisfied, Reason: "Verified"}}, CreatedAt: now}
	first, err := evaluations.Save(ctx, item, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := evaluations.Save(ctx, item, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected same evaluation, first=%s second=%s", first.ID, second.ID)
	}
	loaded, err := goals.Get(ctx, goal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != goaldom.StatusCompleted || loaded.CompletedAt == nil {
		t.Fatalf("goal was not completed: %+v", loaded)
	}
	if err := goals.Update(ctx, loaded); err == nil {
		t.Fatal("completed goal must not be editable")
	}
	items, err := evaluations.List(ctx, goal.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one evaluation, got %d", len(items))
	}
}
