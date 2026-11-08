package agentcatalog

import (
	"context"
	"errors"
	dom "github.com/thkx/agentmesh/internal/domain/agent"
	"testing"
	"time"
)

type fakeRepo struct {
	d      dom.Definition
	runs   []RunRecord
	result dom.Result
}

func (f *fakeRepo) Create(_ context.Context, d dom.Definition) error { f.d = d; return nil }
func (f *fakeRepo) Get(_ context.Context, id string) (dom.Definition, error) {
	if id != f.d.ID {
		return dom.Definition{}, ErrNotFound
	}
	return f.d, nil
}
func (f *fakeRepo) List(context.Context) ([]dom.Definition, error)   { return []dom.Definition{f.d}, nil }
func (f *fakeRepo) Update(_ context.Context, d dom.Definition) error { f.d = d; return nil }
func (f *fakeRepo) Delete(context.Context, string) error             { return nil }
func (f *fakeRepo) RecordRun(_ context.Context, r RunRecord) error {
	f.runs = append(f.runs, r)
	return nil
}
func (f *fakeRepo) FinishRun(_ context.Context, _ string, r dom.Result) error {
	f.result = r
	return nil
}
func (f *fakeRepo) GetRun(context.Context, string) (RunRecord, error) {
	return RunRecord{}, ErrNotFound
}
func (f *fakeRepo) ListRuns(context.Context, string, int) ([]RunRecord, error) { return f.runs, nil }

type fakeRunner struct{}

func (fakeRunner) Run(_ context.Context, t dom.Task) (dom.Result, error) {
	if t.SystemPrompt != "system" || t.Prompt != "do work" || t.Temperature == nil || *t.Temperature != 0.4 {
		return dom.Result{}, errors.New("task mapping failed")
	}
	return dom.Result{TaskID: t.ID, Status: dom.StatusSucceeded, FinishedAt: time.Now()}, nil
}
func TestServiceRunDefinition(t *testing.T) {
	r := &fakeRepo{d: dom.Definition{ID: "a1", Name: "agent", SystemPrompt: "system", ModelID: "m1", Temperature: 0.4, Enabled: true}}
	s, err := NewService(r, fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Run(context.Background(), "a1", "ws1", "do work")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != dom.StatusSucceeded || len(r.runs) != 1 || r.result.TaskID != got.TaskID {
		t.Fatalf("unexpected result: %#v %#v", got, r)
	}
}
