package agentcatalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
)

type Runner interface {
	Run(context.Context, dom.Task) (dom.Result, error)
}
type Service struct {
	repo   Repository
	runner Runner
}

func NewService(repo Repository, runner Runner) (*Service, error) {
	if repo == nil || runner == nil {
		return nil, errors.New("agent catalog dependency is nil")
	}
	return &Service{repo: repo, runner: runner}, nil
}
func (s *Service) Create(ctx context.Context, d dom.Definition) error { return s.repo.Create(ctx, d) }
func (s *Service) Get(ctx context.Context, id string) (dom.Definition, error) {
	return s.repo.Get(ctx, id)
}
func (s *Service) List(ctx context.Context) ([]dom.Definition, error) { return s.repo.List(ctx) }
func (s *Service) Update(ctx context.Context, d dom.Definition) error { return s.repo.Update(ctx, d) }
func (s *Service) Delete(ctx context.Context, id string) error        { return s.repo.Delete(ctx, id) }
func (s *Service) GetRun(ctx context.Context, id string) (RunRecord, error) {
	return s.repo.GetRun(ctx, id)
}
func (s *Service) ListRuns(ctx context.Context, definitionID string, limit int) ([]RunRecord, error) {
	if _, err := s.repo.Get(ctx, definitionID); err != nil {
		return nil, err
	}
	return s.repo.ListRuns(ctx, definitionID, limit)
}
func (s *Service) Run(ctx context.Context, definitionID, workspaceID, prompt string) (dom.Result, error) {
	d, err := s.repo.Get(ctx, definitionID)
	if err != nil {
		return dom.Result{}, err
	}
	if !d.Enabled {
		return dom.Result{}, errors.New("agent definition is disabled")
	}
	taskID, err := newID()
	if err != nil {
		return dom.Result{}, err
	}
	runID, err := newID()
	if err != nil {
		return dom.Result{}, err
	}
	temperature := d.Temperature
	task := dom.Task{ID: taskID, ModelID: d.ModelID, WorkspaceID: workspaceID, SystemPrompt: d.SystemPrompt, Temperature: &temperature, Prompt: prompt, MaxSteps: d.MaxSteps, MaxToolCalls: d.MaxToolCalls, MaxOutputTokens: d.MaxOutputTokens, TimeoutSeconds: d.TimeoutSeconds, ApprovedTools: append([]string(nil), d.ApprovedTools...)}
	if err = task.Validate(); err != nil {
		return dom.Result{}, err
	}
	now := time.Now().UTC()
	if err = s.repo.RecordRun(ctx, RunRecord{ID: runID, DefinitionID: d.ID, TaskID: taskID, Status: "running", CreatedAt: now}); err != nil {
		return dom.Result{}, fmt.Errorf("record agent run: %w", err)
	}
	result, runErr := s.runner.Run(ctx, task)
	if result.TaskID == "" {
		result.TaskID = taskID
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = time.Now().UTC()
	}
	if runErr != nil {
		if result.Error == "" {
			result.Error = runErr.Error()
		}
		result.Status = dom.StatusFailed
	}
	if result.Status == "" {
		result.Status = dom.StatusFailed
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	if finishErr := s.repo.FinishRun(finishCtx, runID, result); finishErr != nil {
		persistErr := fmt.Errorf("persist agent run result: %w", finishErr)
		if runErr != nil {
			runErr = errors.Join(runErr, persistErr)
		} else {
			runErr = persistErr
		}
	}
	return result, runErr
}
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
