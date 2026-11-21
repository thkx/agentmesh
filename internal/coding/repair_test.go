package coding

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
	"github.com/thkx/agentmesh/internal/workspace"
)

type approvingReviewAgent struct{}

func (approvingReviewAgent) Run(context.Context, dom.Task) (dom.Result, error) {
	return dom.Result{Status: dom.StatusSucceeded, Content: `{"verdict":"approve","findings":[]}`}, nil
}

func TestCodingTaskRepairsFailedDockerTestAndPersistsEvidence(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repoPath := makeGitRepo(t)
	root := t.TempDir()
	workspaceManager, err := workspace.NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(testRepository(t), fakeCodingAgent{workspaces: workspaceManager}, root, "golang:1.27.3", false)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err = service.SetAllowedRepositoryRoots([]string{repoPath}); err != nil {
		t.Fatal(err)
	}
	service.SetReviewer(approvingReviewAgent{})

	counter := filepath.Join(t.TempDir(), "docker-test-count")
	dockerPath := filepath.Join(t.TempDir(), "docker")
	script := "#!/bin/sh\ncount=0\nif [ -f \"" + counter + "\" ]; then count=$(cat \"" + counter + "\"); fi\ncount=$((count+1))\nprintf '%s' \"$count\" > \"" + counter + "\"\nif [ \"$count\" -eq 1 ]; then echo 'intentional test failure' >&2; exit 1; fi\necho 'tests passed'\n"
	if err = os.WriteFile(dockerPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	service.sandbox.DockerPath = dockerPath

	task, err := service.Create(context.Background(), CreateRequest{
		ID: "repair-loop", RepositoryPath: repoPath, ModelID: "test", Prompt: "implement the requested change",
		TestCommand:       Command{Program: "go", Args: []string{"test", "./..."}},
		MaxRepairAttempts: 1, TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSucceeded || result.RepairAttempts != 1 {
		t.Fatalf("repair loop did not complete: status=%s repairs=%d error=%q", result.Status, result.RepairAttempts, result.Error)
	}
	steps, err := service.Steps(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var testStatuses []string
	repairFound, reviewApproved := false, false
	for _, step := range steps {
		switch step.Kind {
		case "test.run":
			testStatuses = append(testStatuses, step.Status)
		case "agent.repair":
			repairFound = step.Status == "succeeded"
		case "review.inspect":
			reviewApproved = step.Status == "approve"
		}
	}
	if len(testStatuses) != 2 || testStatuses[0] != "failed" || testStatuses[1] != "succeeded" {
		t.Fatalf("expected failed then successful Docker test evidence, got %v", testStatuses)
	}
	if !repairFound || !reviewApproved {
		t.Fatalf("repair/review evidence missing: repair=%v reviewer_approved=%v", repairFound, reviewApproved)
	}
	events, err := service.Events(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	repairStarted, testPassed := false, false
	for _, event := range events {
		repairStarted = repairStarted || event.Type == "repair.started"
		testPassed = testPassed || event.Type == "test.passed"
	}
	if !repairStarted || !testPassed {
		t.Fatalf("repair/test events missing: repair_started=%v test_passed=%v", repairStarted, testPassed)
	}
}
