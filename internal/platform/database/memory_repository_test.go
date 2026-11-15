package database

import (
	"context"
	"database/sql"
	"errors"
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

func TestMemoryRepositoryScopeIsolationRejectAndTouch(t *testing.T) {
	ctx := context.Background()
	db := newGoalTestDB(t)
	goals, err := NewGoalRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewMemoryRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	createMemorySource(t, ctx, db, goals, "scope-goal-a", "scope-plan-a", "scope-run-a", "scope-eval-a", "scope-ref-a", now)
	createMemorySource(t, ctx, db, goals, "scope-goal-b", "scope-plan-b", "scope-run-b", "scope-eval-b", "scope-ref-b", now)

	items := []dom.Memory{
		newMemoryCandidate("scope-global", "scope-goal-a", "scope-ref-a", "scope-run-a", 0, "global retry", now),
		newMemoryCandidate("scope-goal-a-memory", "scope-goal-a", "scope-ref-a", "scope-run-a", 1, "goal A memory", now),
		newMemoryCandidate("scope-goal-b-memory", "scope-goal-b", "scope-ref-b", "scope-run-b", 0, "goal B memory", now),
		newMemoryCandidate("scope-agent-developer", "scope-goal-a", "scope-ref-a", "scope-run-a", 2, "developer memory", now),
		newMemoryCandidate("scope-agent-reviewer", "scope-goal-a", "scope-ref-a", "scope-run-a", 3, "reviewer memory", now),
		newMemoryCandidate("scope-candidate", "scope-goal-a", "scope-ref-a", "scope-run-a", 4, "candidate memory", now),
		newMemoryCandidate("scope-rejected", "scope-goal-a", "scope-ref-a", "scope-run-a", 5, "rejected memory", now),
		newMemoryCandidate("scope-archived", "scope-goal-a", "scope-ref-a", "scope-run-a", 6, "archived memory", now),
	}
	if _, err := repo.IngestCandidates(ctx, items); err != nil {
		t.Fatalf("ingest candidates: %v", err)
	}

	promote := func(id string, scope dom.ScopeType, scopeID string) {
		t.Helper()
		if _, err := repo.Promote(ctx, id, dom.TypeSemantic, scope, scopeID, "verified by test", now.Add(time.Second)); err != nil {
			t.Fatalf("promote %s: %v", id, err)
		}
	}
	promote("scope-global", dom.ScopeGlobal, "")
	promote("scope-goal-a-memory", dom.ScopeGoal, "scope-goal-a")
	promote("scope-goal-b-memory", dom.ScopeGoal, "scope-goal-b")
	promote("scope-agent-developer", dom.ScopeAgent, "developer")
	promote("scope-agent-reviewer", dom.ScopeAgent, "reviewer")
	if _, err := repo.Reject(ctx, "scope-rejected", "not sufficiently supported", now.Add(2*time.Second)); err != nil {
		t.Fatalf("reject candidate: %v", err)
	}
	if _, err := repo.Promote(ctx, "scope-archived", dom.TypeEpisodic, dom.ScopeGoal, "scope-goal-a", "approved", now.Add(time.Second)); err != nil {
		t.Fatalf("promote memory to archive: %v", err)
	}
	if _, err := repo.Archive(ctx, "scope-archived", now.Add(3*time.Second)); err != nil {
		t.Fatalf("archive memory: %v", err)
	}

	assertIDs := func(label string, got []dom.Memory, want ...string) {
		t.Helper()
		actual := make(map[string]bool, len(got))
		for _, item := range got {
			actual[item.ID] = true
		}
		if len(actual) != len(want) {
			t.Fatalf("%s: expected %d memories, got %v", label, len(want), actual)
		}
		for _, id := range want {
			if !actual[id] {
				t.Errorf("%s: expected memory %q in result %v", label, id, actual)
			}
		}
	}

	goalA, err := repo.SearchActive(ctx, "scope-goal-a", nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs("goal A without agent scope", goalA, "scope-global", "scope-goal-a-memory")
	goalB, err := repo.SearchActive(ctx, "scope-goal-b", nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs("goal B without agent scope", goalB, "scope-global", "scope-goal-b-memory")
	developer, err := repo.SearchActive(ctx, "scope-goal-a", []string{"developer"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs("developer profile scope", developer, "scope-global", "scope-goal-a-memory", "scope-agent-developer")
	reviewer, err := repo.SearchActive(ctx, "scope-goal-a", []string{"reviewer"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs("reviewer profile scope", reviewer, "scope-global", "scope-goal-a-memory", "scope-agent-reviewer")

	for _, id := range []string{"scope-candidate", "scope-rejected", "scope-archived", "scope-goal-b-memory", "scope-agent-reviewer"} {
		for _, item := range developer {
			if item.ID == id {
				t.Fatalf("memory %q leaked into developer scope", id)
			}
		}
	}

	touchedAt := now.Add(10 * time.Second)
	if err := repo.Touch(ctx, []string{"scope-global", "scope-global"}, touchedAt); err != nil {
		t.Fatalf("touch memory: %v", err)
	}
	stored, err := repo.Get(ctx, "scope-global")
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccessCount != 2 {
		t.Fatalf("expected two persisted accesses, got %d", stored.AccessCount)
	}
	if !stored.LastAccessedAt.Equal(touchedAt) {
		t.Fatalf("expected last access %s, got %s", touchedAt, stored.LastAccessedAt)
	}

	rejected, err := repo.Get(ctx, "scope-rejected")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Status != dom.StatusRejected || rejected.RejectionReason != "not sufficiently supported" {
		t.Fatalf("reject metadata was not persisted: %#v", rejected)
	}
	if _, err := repo.Promote(ctx, "scope-rejected", dom.TypeSemantic, dom.ScopeGlobal, "", "must not revive", now.Add(4*time.Second)); !errors.Is(err, dom.ErrInvalidState) {
		t.Fatalf("expected rejected memory promotion to fail with invalid state, got %v", err)
	}
	if _, err := repo.Reject(ctx, "scope-rejected", "second rejection", now.Add(4*time.Second)); !errors.Is(err, dom.ErrInvalidState) {
		t.Fatalf("expected repeated rejection to fail with invalid state, got %v", err)
	}
	if _, err := repo.Archive(ctx, "scope-candidate", now.Add(4*time.Second)); !errors.Is(err, dom.ErrInvalidState) {
		t.Fatalf("expected candidate archival to fail with invalid state, got %v", err)
	}
	if _, err := repo.Promote(ctx, "scope-archived", dom.TypeSemantic, dom.ScopeGlobal, "", "must not revive", now.Add(4*time.Second)); !errors.Is(err, dom.ErrInvalidState) {
		t.Fatalf("expected archived memory promotion to fail with invalid state, got %v", err)
	}
}

func TestMemoryRepositoryConcurrentPromotionHasSingleWinner(t *testing.T) {
	ctx := context.Background()
	db := newGoalTestDB(t)
	goals, err := NewGoalRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewMemoryRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	createMemorySource(t, ctx, db, goals, "concurrent-goal", "concurrent-plan", "concurrent-run", "concurrent-eval", "concurrent-ref", now)
	candidate := newMemoryCandidate("concurrent-memory", "concurrent-goal", "concurrent-ref", "concurrent-run", 0, "concurrent promotion", now)
	if _, err := repo.IngestCandidates(ctx, []dom.Memory{candidate}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-start
			_, promoteErr := repo.Promote(ctx, candidate.ID, dom.TypeSemantic, dom.ScopeGlobal, "", "concurrent approval", now.Add(time.Duration(i+1)*time.Second))
			results <- promoteErr
		}(i)
	}
	close(start)
	first, second := <-results, <-results
	successes, invalidStates := 0, 0
	for _, err := range []error{first, second} {
		if err == nil {
			successes++
		} else if errors.Is(err, dom.ErrInvalidState) {
			invalidStates++
		} else {
			t.Fatalf("unexpected promotion error: %v", err)
		}
	}
	if successes != 1 || invalidStates != 1 {
		t.Fatalf("expected exactly one promotion winner, success=%d invalid_state=%d", successes, invalidStates)
	}
}

func TestMemoryRepositoryRejectsMismatchedReflectionSource(t *testing.T) {
	ctx := context.Background()
	db := newGoalTestDB(t)
	goals, err := NewGoalRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewMemoryRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	createMemorySource(t, ctx, db, goals, "source-guard-goal", "source-guard-plan", "source-guard-run", "source-guard-eval", "source-guard-ref", now)
	candidate := newMemoryCandidate("source-guard-memory", "source-guard-goal", "does-not-exist", "source-guard-run", 0, "invalid source", now)
	if _, err := repo.IngestCandidates(ctx, []dom.Memory{candidate}); err == nil {
		t.Fatal("expected database source-reflection guard to reject mismatched source")
	}
}

func createMemorySource(t *testing.T, ctx context.Context, db *sql.DB, goals *GoalRepository, goalID, planID, runID, evaluationID, reflectionID string, now time.Time) {
	t.Helper()
	goal := goaldom.Goal{ID: goalID, Description: "memory repository integration test", Status: goaldom.StatusActive, CreatedAt: now, UpdatedAt: now}
	if err := goals.Create(ctx, goal); err != nil {
		t.Fatalf("create goal %s: %v", goalID, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO agent_plans(id,goal_id,version,objective,steps_json,status,model_id,created_at) VALUES(?,?,?,?,?,?,?,?)`, planID, goalID, 1, "objective", "[]", "validated", "model", now.UnixMilli()); err != nil {
		t.Fatalf("create plan %s: %v", planID, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO plan_executions(goal_id,plan_id,plan_version,workflow_id,workflow_version,run_id,input_hash,created_at) VALUES(?,?,?,?,?,?,?,?)`, goalID, planID, 1, "workflow-"+runID, 1, runID, "hash-"+runID, now.Unix()); err != nil {
		t.Fatalf("create plan execution %s: %v", runID, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO goal_evaluations(id,goal_id,run_id,model_id,execution_status,status,summary,criteria_json,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, evaluationID, goalID, runID, "model", "succeeded", "satisfied", "ok", "[]", now.UnixMilli()); err != nil {
		t.Fatalf("create evaluation %s: %v", evaluationID, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO goal_reflections(id,goal_id,run_id,evaluation_id,model_id,summary,insights_json,created_at) VALUES(?,?,?,?,?,?,?,?)`, reflectionID, goalID, runID, evaluationID, "model", "summary", "[]", now.UnixMilli()); err != nil {
		t.Fatalf("create reflection %s: %v", reflectionID, err)
	}
}

func newMemoryCandidate(id, goalID, reflectionID, runID string, sourceIndex int, title string, now time.Time) dom.Memory {
	return dom.Memory{
		ID: id, Type: dom.TypeEpisodic, Status: dom.StatusCandidate,
		ScopeType: dom.ScopeGoal, ScopeID: goalID, GoalID: goalID,
		SourceReflectionID: reflectionID, SourceRunID: runID, SourceIndex: sourceIndex,
		Title: title, Content: "memory repository scope and lifecycle test content",
		Recommendation: "preserve the verified behavior", Confidence: .8,
		Evidence: []dom.Evidence{{NodeID: "test-node", Quote: "verified source evidence"}},
		Tags:     []string{"test"}, CreatedAt: now, UpdatedAt: now,
	}
}
