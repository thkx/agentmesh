package agentcatalog

import (
	"context"
	"errors"
	dom "github.com/thkx/agentmesh/internal/domain/agent"
	"testing"
)

type catalogRepoFake struct {
	definition dom.Definition
	run        RunRecord
	result     dom.Result
	finishErr  error
}

func (r *catalogRepoFake) Create(context.Context, dom.Definition) error { return nil }
func (r *catalogRepoFake) Get(_ context.Context, id string) (dom.Definition, error) {
	if id != r.definition.ID {
		return dom.Definition{}, ErrNotFound
	}
	return r.definition, nil
}
func (r *catalogRepoFake) List(context.Context) ([]dom.Definition, error) {
	return []dom.Definition{r.definition}, nil
}
func (r *catalogRepoFake) Update(context.Context, dom.Definition) error   { return nil }
func (r *catalogRepoFake) Delete(context.Context, string) error           { return nil }
func (r *catalogRepoFake) RecordRun(_ context.Context, v RunRecord) error { r.run = v; return nil }
func (r *catalogRepoFake) FinishRun(_ context.Context, id string, v dom.Result) error {
	if id != r.run.ID {
		return ErrNotFound
	}
	if r.finishErr != nil {
		return r.finishErr
	}
	r.result = v
	return nil
}
func (r *catalogRepoFake) GetRun(context.Context, string) (RunRecord, error) { return r.run, nil }
func (r *catalogRepoFake) ListRuns(context.Context, string, int) ([]RunRecord, error) {
	return []RunRecord{r.run}, nil
}

type catalogRunnerFake func(context.Context, dom.Task) (dom.Result, error)

func (f catalogRunnerFake) Run(ctx context.Context, t dom.Task) (dom.Result, error) { return f(ctx, t) }
func newCatalogTestService(t *testing.T, runner Runner, repo *catalogRepoFake) *Service {
	t.Helper()
	s, err := NewService(repo, runner)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func validDefinition() dom.Definition {
	return dom.Definition{ID: "agent-1", Name: "Test Agent", SystemPrompt: "system", ModelID: "model-1", Enabled: true, MaxSteps: 4, MaxToolCalls: 4, TimeoutSeconds: 30}
}

func TestRunPersistsFailedStatusWhenRunnerFails(t *testing.T) {
	repo := &catalogRepoFake{definition: validDefinition()}
	want := errors.New("runtime failed")
	service := newCatalogTestService(t, catalogRunnerFake(func(context.Context, dom.Task) (dom.Result, error) { return dom.Result{}, want }), repo)
	result, err := service.Run(context.Background(), "agent-1", "workspace-1", "do work")
	if !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
	if result.Status != dom.StatusFailed || repo.result.Status != dom.StatusFailed {
		t.Fatalf("result=%s persisted=%s", result.Status, repo.result.Status)
	}
}

func TestRunPersistsCanceledAndTimedOutStatuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		want  dom.Status
	}{{"canceled", context.Canceled, dom.StatusCanceled}, {"timed_out", context.DeadlineExceeded, dom.StatusTimedOut}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &catalogRepoFake{definition: validDefinition()}
			service := newCatalogTestService(t, catalogRunnerFake(func(context.Context, dom.Task) (dom.Result, error) { return dom.Result{}, tc.cause }), repo)
			result, err := service.Run(context.Background(), "agent-1", "workspace-1", "do work")
			if !errors.Is(err, tc.cause) {
				t.Fatalf("error=%v", err)
			}
			if result.Status != tc.want || repo.result.Status != tc.want {
				t.Fatalf("result=%s persisted=%s want=%s", result.Status, repo.result.Status, tc.want)
			}
		})
	}
}

func TestRunReturnsFinishPersistenceFailure(t *testing.T) {
	repo := &catalogRepoFake{definition: validDefinition(), finishErr: errors.New("disk unavailable")}
	service := newCatalogTestService(t, catalogRunnerFake(func(context.Context, dom.Task) (dom.Result, error) {
		return dom.Result{Status: dom.StatusSucceeded, Content: "done"}, nil
	}), repo)
	_, err := service.Run(context.Background(), "agent-1", "workspace-1", "do work")
	if err == nil {
		t.Fatal("expected finish persistence error")
	}
}

func TestRunFinishPersistenceUsesIndependentBoundedContext(t *testing.T) {
	repo := &catalogRepoFake{definition: validDefinition()}
	service := newCatalogTestService(t, catalogRunnerFake(func(ctx context.Context, _ dom.Task) (dom.Result, error) {
		return dom.Result{}, ctx.Err()
	}), repo)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Run(ctx, "agent-1", "workspace-1", "do work")
	if err == nil {
		t.Fatal("expected canceled runner result")
	}
	if repo.result.Status != dom.StatusCanceled {
		t.Fatalf("persisted status=%s", repo.result.Status)
	}
}
