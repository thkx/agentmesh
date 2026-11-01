package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/thkx/agentmesh/internal/agent/executor/cli/process"
	"github.com/thkx/agentmesh/internal/agent/executor/cli/protocol"
	"github.com/thkx/agentmesh/internal/domain/execution"
)

type WorkspaceLeaser interface {
	AcquirePath(path string) (func(), error)
}

type CLIExecutor struct {
	leaser        WorkspaceLeaser
	adapters      map[execution.Provider]Adapter
	workspaceRoot string
	runner        process.Runner
	mu            sync.Mutex
	active        map[string]context.CancelFunc
}

func New(adapters map[execution.Provider]Adapter, workspaceRoot string, leasers ...WorkspaceLeaser) *CLIExecutor {
	root, _ := filepath.Abs(workspaceRoot)
	var leaser WorkspaceLeaser
	if len(leasers) > 0 {
		leaser = leasers[0]
	}
	return &CLIExecutor{adapters: adapters, workspaceRoot: root, leaser: leaser, active: make(map[string]context.CancelFunc)}
}
func (e *CLIExecutor) Execute(parent context.Context, request execution.Request) (*execution.Result, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	adapter, ok := e.adapters[request.Provider]
	if !ok {
		return nil, execution.ErrUnsupportedProvider
	}
	workspacePath, pathErr := filepath.Abs(request.WorkspacePath)
	if pathErr != nil {
		return nil, pathErr
	}
	workspacePath, pathErr = filepath.EvalSymlinks(workspacePath)
	if pathErr != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", pathErr)
	}
	info, statErr := os.Stat(workspacePath)
	if statErr != nil {
		return nil, fmt.Errorf("workspace unavailable: %w", statErr)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace path is not a directory")
	}
	if strings.TrimSpace(e.workspaceRoot) == "" {
		return nil, fmt.Errorf("workspace root is not configured")
	}
	rootPath, rootErr := filepath.EvalSymlinks(e.workspaceRoot)
	if rootErr != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", rootErr)
	}
	rel, relErr := filepath.Rel(rootPath, workspacePath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("workspace path must be inside configured workspace root")
	}
	request.WorkspacePath = workspacePath
	if e.leaser != nil {
		release, leaseErr := e.leaser.AcquirePath(workspacePath)
		if leaseErr != nil {
			return nil, fmt.Errorf("acquire workspace lease: %w", leaseErr)
		}
		defer release()
	}
	spec, err := adapter.Build(request)
	if err != nil {
		return nil, err
	}
	if request.Timeout == 0 {
		request.Timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, request.Timeout)
	e.mu.Lock()
	if _, exists := e.active[request.ID]; exists {
		e.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("execution %q is already running", request.ID)
	}
	e.active[request.ID] = cancel
	e.mu.Unlock()
	defer func() { e.mu.Lock(); delete(e.active, request.ID); e.mu.Unlock(); cancel() }()
	started := time.Now()
	output, runErr := e.runner.Run(ctx, spec)
	finished := time.Now()
	status := execution.StatusSucceeded
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status = execution.StatusTimedOut
	} else if errors.Is(ctx.Err(), context.Canceled) {
		status = execution.StatusCanceled
	} else if runErr != nil || output.ExitCode != 0 {
		status = execution.StatusFailed
	}
	result := &execution.Result{ID: request.ID, Provider: request.Provider, Status: status, Output: protocol.ExtractText([]byte(output.Stdout)), SessionID: protocol.ExtractSessionID([]byte(output.Stdout)), Stderr: output.Stderr, ExitCode: output.ExitCode, StartedAt: started, FinishedAt: finished, Duration: finished.Sub(started)}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if runErr != nil {
		return result, fmt.Errorf("coding agent execution %s: %w", status, runErr)
	}
	if output.ExitCode != 0 {
		return result, fmt.Errorf("coding agent exited with code %d", output.ExitCode)
	}
	return result, nil
}
func (e *CLIExecutor) Cancel(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	cancel, ok := e.active[id]
	e.mu.Unlock()
	if !ok {
		return execution.ErrExecutionNotFound
	}
	cancel()
	return nil
}
