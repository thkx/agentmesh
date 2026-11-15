package database

import (
	"context"
	"testing"
	"time"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	evaldom "github.com/thkx/agentmesh/internal/domain/goalevaluation"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
	dom "github.com/thkx/agentmesh/internal/domain/reflection"
)

func TestReflectionRepositoryAppendOnlyAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db := newGoalTestDB(t)
	goals, _ := NewGoalRepository(db)
	plans, _ := NewPlanRepository(db)
	evaluations, _ := NewGoalEvaluationRepository(db)
	reflections, err := NewReflectionRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	goal := goaldom.Goal{ID: "reflection-goal", Description: "deliver feature", Status: goaldom.StatusActive, SuccessCriteria: []goaldom.SuccessCriterion{{ID: "tests", Description: "Tests pass", Required: true}}, CreatedAt: now, UpdatedAt: now}
	if err := goals.Create(ctx, goal); err != nil {
		t.Fatal(err)
	}
	plan, err := plans.Create(ctx, pland.Plan{ID: "reflection-plan", GoalID: goal.ID, Objective: "implement", Steps: []pland.Step{{ID: "end", Name: "End", Description: "End", Type: pland.StepEnd}}, Status: pland.PlanValidated, ModelID: "model", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO plan_executions(goal_id,plan_id,plan_version,workflow_id,workflow_version,run_id,input_hash,created_at) VALUES(?,?,?,?,?,?,?,?)`, goal.ID, plan.ID, plan.Version, "wf", 1, "reflection-run", "hash", now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err = evaluations.Save(ctx, evaldom.Evaluation{ID: "reflection-eval", GoalID: goal.ID, RunID: "reflection-run", ModelID: "model", ExecutionStatus: "succeeded", Status: evaldom.StatusSatisfied, Summary: "Goal achieved", Criteria: []evaldom.CriterionResult{{CriterionID: "tests", Status: evaldom.StatusSatisfied, Reason: "verified"}}, CreatedAt: now}, true); err != nil {
		t.Fatal(err)
	}
	item := dom.Reflection{ID: "reflection-1", GoalID: goal.ID, RunID: "reflection-run", EvaluationID: "reflection-eval", ModelID: "model", Summary: "Tests-first workflow was effective", Insights: []dom.Insight{{Kind: dom.InsightSuccessPattern, Title: "Verify early", Description: "Early verification reduced rework", Confidence: .9, Evidence: []dom.Evidence{{NodeID: "tests", Quote: "all tests passed"}}}}, CreatedAt: now}
	first, err := reflections.Save(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	item.ID = "reflection-duplicate"
	second, err := reflections.Save(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected idempotent reflection, got %s and %s", first.ID, second.ID)
	}
	if _, err = db.ExecContext(ctx, `UPDATE goal_reflections SET summary='mutated' WHERE id=?`, first.ID); err == nil {
		t.Fatal("expected append-only update trigger")
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM goal_reflections WHERE id=?`, first.ID); err == nil {
		t.Fatal("expected append-only delete trigger")
	}
	loaded, err := reflections.Get(ctx, goal.ID, "reflection-run")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != first.ID || len(loaded.Insights) != 1 {
		t.Fatalf("unexpected reflection: %+v", loaded)
	}
	items, err := reflections.List(ctx, goal.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one reflection, got %d", len(items))
	}
}
