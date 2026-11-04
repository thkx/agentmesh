package database

import (
	"context"
	"testing"
	"time"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	dom "github.com/thkx/agentmesh/internal/domain/memory"
)

func TestMemoryRepositoryLifecycleAndScopeRetrieval(t *testing.T) {
	ctx := context.Background()
	db := newGoalTestDB(t)
	goals, _ := NewGoalRepository(db)
	repo, err := NewMemoryRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	goal := goaldom.Goal{ID: "memory-goal", Description: "memory test", Status: goaldom.StatusActive, CreatedAt: now, UpdatedAt: now}
	if err := goals.Create(ctx, goal); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO agent_plans(id,goal_id,version,objective,steps_json,status,model_id,created_at) VALUES(?,?,?,?,?,?,?,?)`, "p1", goal.ID, 1, "objective", "[]", "validated", "model", now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO plan_executions(goal_id,plan_id,plan_version,workflow_id,workflow_version,run_id,input_hash,created_at) VALUES(?,?,?,?,?,?,?,?)`, goal.ID, "p1", 1, "wf", 1, "run1", "hash", now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO goal_evaluations(id,goal_id,run_id,model_id,execution_status,status,summary,criteria_json,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, "eval1", goal.ID, "run1", "model", "succeeded", "satisfied", "ok", "[]", now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO goal_reflections(id,goal_id,run_id,evaluation_id,model_id,summary,insights_json,created_at) VALUES(?,?,?,?,?,?,?,?)`, "ref1", goal.ID, "run1", "eval1", "model", "summary", "[]", now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	item := dom.Memory{ID: "mem1", Type: dom.TypeEpisodic, Status: dom.StatusCandidate, ScopeType: dom.ScopeGoal, ScopeID: goal.ID, GoalID: goal.ID, SourceReflectionID: "ref1", SourceRunID: "run1", SourceIndex: 0, Title: "Retry timeout", Content: "Retry transient API timeout with bounded backoff", Recommendation: "Use bounded exponential backoff", Confidence: .9, Evidence: []dom.Evidence{{NodeID: "n1", Quote: "request timed out"}}, Tags: []string{"improvement"}, CreatedAt: now, UpdatedAt: now}
	created, err := repo.IngestCandidates(ctx, []dom.Memory{item})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0].Status != dom.StatusCandidate {
		t.Fatalf("unexpected ingest result: %#v", created)
	}
	duplicate := item
	duplicate.ID = "mem2"
	again, err := repo.IngestCandidates(ctx, []dom.Memory{duplicate})
	if err != nil {
		t.Fatal(err)
	}
	if again[0].ID != "mem1" {
		t.Fatalf("ingest must be idempotent: %#v", again)
	}
	active, err := repo.Promote(ctx, "mem1", dom.TypeProcedural, dom.ScopeGlobal, "", "reviewed evidence", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != dom.StatusActive || active.Type != dom.TypeProcedural {
		t.Fatalf("unexpected promotion: %#v", active)
	}
	found, err := repo.SearchActive(ctx, goal.ID, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("expected global memory, got %d", len(found))
	}
	if err := repo.Touch(ctx, []string{"mem1"}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	active, err = repo.Get(ctx, "mem1")
	if err != nil {
		t.Fatal(err)
	}
	if active.AccessCount != 1 {
		t.Fatalf("expected access count 1, got %d", active.AccessCount)
	}
	archived, err := repo.Archive(ctx, "mem1", now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != dom.StatusArchived {
		t.Fatalf("unexpected archived status: %s", archived.Status)
	}
	found, err = repo.SearchActive(ctx, goal.ID, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatal("archived memory must not be searchable")
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM agent_memories WHERE id='mem1'`); err == nil {
		t.Fatal("expected memory retention trigger")
	}
	if _, err = db.ExecContext(ctx, `UPDATE agent_memories SET content='tampered' WHERE id='mem1'`); err == nil {
		t.Fatal("expected immutable content trigger")
	}
	if _, err = db.ExecContext(ctx, `UPDATE agent_memories SET memory_type='semantic' WHERE id='mem1'`); err == nil {
		t.Fatal("expected immutable classification trigger")
	}
}
