package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
	_ "modernc.org/sqlite"
)

func newGoalTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:goal_repository_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestGoalAndPlanRepositories(t *testing.T) {
	ctx := context.Background()
	db := newGoalTestDB(t)
	goals, err := NewGoalRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := NewPlanRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	g := goaldom.Goal{ID: "goal-1", Description: "Research a developer product", Constraints: []string{"solo developer"}, SuccessCriteria: []goaldom.SuccessCriterion{{ID: "market", Description: "Market analysis exists", Required: true}}, Context: map[string]any{"platform": "cross-platform"}, Status: goaldom.StatusActive, CreatedAt: now, UpdatedAt: now}
	if err := goals.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	loaded, err := goals.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Description != g.Description || len(loaded.SuccessCriteria) != 1 {
		t.Fatalf("unexpected goal: %+v", loaded)
	}
	loaded.Description = "Updated research goal"
	loaded.UpdatedAt = now.Add(500 * time.Millisecond)
	if err := goals.Update(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = goals.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Description != "Updated research goal" {
		t.Fatalf("goal update was not persisted: %+v", loaded)
	}
	p := pland.Plan{ID: "plan-1", GoalID: g.ID, Objective: "Research and report", Steps: []pland.Step{{ID: "research", Name: "Research", Description: "Collect sources", Type: pland.StepAgent, AgentProfile: "research"}}, Status: pland.PlanValidated, ModelID: "model-1", CreatedAt: now}
	created, err := plans.Create(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if created.Version != 1 {
		t.Fatalf("expected version 1, got %d", created.Version)
	}
	p.ID = "plan-2"
	created, err = plans.Create(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if created.Version != 2 {
		t.Fatalf("expected version 2, got %d", created.Version)
	}
	latest, err := plans.Get(ctx, g.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != 2 {
		t.Fatalf("expected latest version 2, got %d", latest.Version)
	}
	if _, err := plans.Get(ctx, g.ID, 1); err != nil {
		t.Fatalf("get plan v1: %v", err)
	}
	if err := goals.Cancel(ctx, g.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	loaded, err = goals.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != goaldom.StatusCanceled {
		t.Fatalf("expected canceled goal, got %s", loaded.Status)
	}
}
