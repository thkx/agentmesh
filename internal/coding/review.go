package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	agentdom "github.com/thkx/agentmesh/internal/domain/agent"
)

type ReviewVerdict struct {
	Verdict  string   `json:"verdict"`
	Findings []string `json:"findings,omitempty"`
}

func parseReviewVerdict(content string) (ReviewVerdict, error) {
	content = strings.TrimSpace(content)
	start, end := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if start < 0 || end < start { return ReviewVerdict{}, errors.New("reviewer did not return a JSON verdict") }
	var verdict ReviewVerdict
	if err := json.Unmarshal([]byte(content[start:end+1]), &verdict); err != nil { return ReviewVerdict{}, fmt.Errorf("decode reviewer verdict: %w", err) }
	if verdict.Verdict != "approve" && verdict.Verdict != "request_changes" { return ReviewVerdict{}, errors.New("reviewer verdict must be approve or request_changes") }
	if len(verdict.Findings) > 50 { verdict.Findings = verdict.Findings[:50] }
	for i := range verdict.Findings { if len(verdict.Findings[i]) > 2000 { verdict.Findings[i] = verdict.Findings[i][:2000] } }
	return verdict, nil
}

func commandExitCode(err error) int {
	if err == nil { return 0 }
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) { return exitErr.ExitCode() }
	return -1
}

// review inspects the actual patch and the recorded test summary. It is a
// separate Agent invocation with no approved write tools and fails closed.
func (s *Service) review(ctx context.Context, task Task, diff string) error {
	s.mu.Lock()
	reviewer := s.reviewer
	s.mu.Unlock()
	if reviewer == nil { return nil }
	started := time.Now().UTC()
	step := Step{TaskID: task.ID, Sequence: 100, Kind: "review.inspect", Status: "running", StartedAt: started}
	_ = s.repo.Event(ctx, task.ID, "review.started", map[string]any{"diff_bytes": len(diff)})
	prompt := "You are the independent code reviewer. Review only the actual patch below. Identify correctness, security, regression, and test-coverage risks. Do not modify files. Return exactly one JSON object: {\"verdict\":\"approve\" or \"request_changes\",\"findings\":[\"...\"]}. Approve only if no blocking issue remains.\n\nTask: " + task.Prompt + "\n\nTest summary:\n" + limit(task.Result, 12000) + "\n\nPatch:\n" + limit(diff, 40000)
	result, err := reviewer.Run(ctx, agentdom.Task{ID: "coding-" + task.ID + "-review", ModelID: task.ModelID, WorkspaceID: task.WorkspaceID, Prompt: prompt, MaxSteps: 8, MaxToolCalls: 12, TimeoutSeconds: task.TimeoutSeconds})
	step.FinishedAt = time.Now().UTC()
	step.DurationMillis = step.FinishedAt.Sub(started).Milliseconds()
	if err != nil {
		step.Status = "failed"
		step.Error = err.Error()
		_ = s.repo.AddStep(context.WithoutCancel(ctx), step)
		_ = s.repo.Event(context.WithoutCancel(ctx), task.ID, "review.failed", map[string]string{"error": limit(err.Error(), 2000)})
		return fmt.Errorf("independent coding review failed: %w", err)
	}
	verdict, err := parseReviewVerdict(result.Content)
	step.Output = result.Content
	if err != nil {
		step.Status = "failed"
		step.Error = err.Error()
		_ = s.repo.AddStep(context.WithoutCancel(ctx), step)
		_ = s.repo.Event(context.WithoutCancel(ctx), task.ID, "review.invalid", map[string]string{"error": err.Error()})
		return err
	}
	step.Status = verdict.Verdict
	if err = s.repo.AddStep(ctx, step); err != nil { return fmt.Errorf("persist reviewer evidence: %w", err) }
	if err = s.repo.Event(ctx, task.ID, "review.finished", verdict); err != nil { return fmt.Errorf("persist reviewer verdict: %w", err) }
	if verdict.Verdict != "approve" {
		return fmt.Errorf("review requested changes: %s", strings.Join(verdict.Findings, "; "))
	}
	return nil
}
