package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/thkx/agentmesh/internal/agent/contextmanager"
	"github.com/thkx/agentmesh/internal/agent/loopguard"
	agentstate "github.com/thkx/agentmesh/internal/agent/state"
	dom "github.com/thkx/agentmesh/internal/domain/agent"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"github.com/thkx/agentmesh/internal/tools"
	"github.com/thkx/agentmesh/internal/workspace"
)

var ErrNilDependency = errors.New("agent runtime dependency is nil")

type Model interface {
	Generate(context.Context, modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error)
}
type ToolCaller interface {
	Invoke(context.Context, string, json.RawMessage, string, bool) (any, error)
	Registry() *tools.Registry
}
type WorkspaceManager interface {
	Resolve(string) (workspace.Workspace, error)
	AcquirePath(string) (func(), error)
}
type ToolLedger interface {
	Begin(context.Context, string, string, string, string) (bool, error)
	Complete(context.Context, string, any, error) error
	Load(context.Context, string) (json.RawMessage, string, bool, error)
}
type Config struct {
	DefaultMaxSteps     int
	DefaultMaxToolCalls int
	MaxContextChars     int
	MaxConcurrent       int
	DefaultTimeout      time.Duration
}
type Runtime struct {
	model      Model
	tool       ToolCaller
	workspaces WorkspaceManager
	cfg        Config
	sem        chan struct{}
	mu         sync.RWMutex
	tasks      map[string]dom.Result
	cancels    map[string]context.CancelFunc
	completed  []string
	stateStore agentstate.Store
	toolLedger ToolLedger
}

func New(model Model, tool ToolCaller, workspaces WorkspaceManager, cfg Config) (*Runtime, error) {
	if model == nil || tool == nil || tool.Registry() == nil || workspaces == nil {
		return nil, ErrNilDependency
	}
	if cfg.DefaultMaxSteps <= 0 {
		cfg.DefaultMaxSteps = 12
	}
	if cfg.DefaultMaxToolCalls <= 0 {
		cfg.DefaultMaxToolCalls = 8
	}
	if cfg.MaxContextChars <= 0 {
		cfg.MaxContextChars = 24000
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 4
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 15 * time.Minute
	}
	return &Runtime{model: model, tool: tool, workspaces: workspaces, cfg: cfg, sem: make(chan struct{}, cfg.MaxConcurrent), tasks: map[string]dom.Result{}, cancels: map[string]context.CancelFunc{}, completed: make([]string, 0, 1000)}, nil
}

// SetToolLedger enables durable tool-call idempotency. Configure before serving requests.
func (r *Runtime) SetToolLedger(ledger ToolLedger) { r.mu.Lock(); r.toolLedger = ledger; r.mu.Unlock() }

// SetStateStore enables durable task and conversation snapshots. It must be called before serving requests.
func (r *Runtime) SetStateStore(store agentstate.Store) {
	r.mu.Lock()
	r.stateStore = store
	r.mu.Unlock()
}

type control struct {
	Type      string          `json:"type"`
	Tool      string          `json:"tool,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Content   string          `json:"content,omitempty"`
}

func (r *Runtime) Run(ctx context.Context, task dom.Task) (result dom.Result, err error) {
	if err = task.Validate(); err != nil {
		return result, err
	}
	select {
	case r.sem <- struct{}{}:
	case <-ctx.Done():
		return result, ctx.Err()
	}
	defer func() { <-r.sem }()
	ws, resolveErr := r.workspaces.Resolve(task.WorkspaceID)
	if resolveErr != nil {
		return result, resolveErr
	}
	release, leaseErr := r.workspaces.AcquirePath(ws.Path)
	if leaseErr != nil {
		return result, leaseErr
	}
	defer release()
	var runCtx context.Context
	var cancel context.CancelFunc
	timeout := r.cfg.DefaultTimeout
	if task.TimeoutSeconds > 0 && time.Duration(task.TimeoutSeconds)*time.Second < timeout {
		timeout = time.Duration(task.TimeoutSeconds) * time.Second
	}
	runCtx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()
	r.mu.RLock()
	existingStore := r.stateStore
	r.mu.RUnlock()
	var previous *agentstate.Snapshot
	if existingStore != nil {
		if snapshot, loadErr := existingStore.Load(ctx, task.ID); loadErr == nil {
			if !reflect.DeepEqual(snapshot.Task, task) || snapshot.Result.Status != dom.StatusFailed || snapshot.Result.ToolCalls != 0 {
				return result, fmt.Errorf("task %q already exists and is not safely retryable", task.ID)
			}
			previous = &snapshot
		} else if !errors.Is(loadErr, agentstate.ErrNotFound) {
			return result, fmt.Errorf("check persisted task: %w", loadErr)
		}
	}
	now := time.Now().UTC()
	if previous != nil && !previous.Result.StartedAt.IsZero() {
		now = previous.Result.StartedAt
	}
	result = dom.Result{TaskID: task.ID, Status: dom.StatusRunning, StartedAt: now}
	r.mu.Lock()
	retrying := previous != nil
	if old, exists := r.tasks[task.ID]; exists {
		if !(old.Status == dom.StatusFailed && old.ToolCalls == 0) {
			r.mu.Unlock()
			return result, fmt.Errorf("task %q already exists and is not safely retryable", task.ID)
		}
		retrying = true
	}
	r.tasks[task.ID] = result
	r.cancels[task.ID] = cancel
	if retrying {
		filtered := r.completed[:0]
		for _, completedID := range r.completed {
			if completedID != task.ID {
				filtered = append(filtered, completedID)
			}
		}
		r.completed = filtered
	}
	r.mu.Unlock()
	var persistSnapshot func() error
	defer func() {
		result.FinishedAt = time.Now().UTC()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				result.Status = dom.StatusTimedOut
			} else if errors.Is(err, context.Canceled) {
				result.Status = dom.StatusCanceled
			} else {
				result.Status = dom.StatusFailed
			}
			result.Error = err.Error()
		} else {
			result.Status = dom.StatusSucceeded
		}
		r.mu.Lock()
		r.tasks[task.ID] = result
		delete(r.cancels, task.ID)
		r.completed = append(r.completed, task.ID)
		if len(r.completed) > 1000 {
			oldest := r.completed[0]
			r.completed = r.completed[1:]
			delete(r.tasks, oldest)
		}
		r.mu.Unlock()
		if persistSnapshot != nil {
			if saveErr := persistSnapshot(); saveErr != nil && err == nil {
				err = fmt.Errorf("persist final agent state: %w", saveErr)
				result.Status = dom.StatusFailed
				result.Error = err.Error()
				r.mu.Lock()
				r.tasks[task.ID] = result
				r.mu.Unlock()
			}
		}
	}()
	maxSteps := r.cfg.DefaultMaxSteps
	if task.MaxSteps > 0 && task.MaxSteps < maxSteps {
		maxSteps = task.MaxSteps
	}
	maxCalls := r.cfg.DefaultMaxToolCalls
	if task.MaxToolCalls > 0 && task.MaxToolCalls < maxCalls {
		maxCalls = task.MaxToolCalls
	}
	guard := loopguard.New(maxSteps, maxCalls)
	messages := []modelgateway.Message{{Role: modelgateway.RoleSystem, Content: r.systemPrompt(task.WorkspaceID, task.ApprovedTools, false)}, {Role: modelgateway.RoleUser, Content: task.Prompt}}
	persistSnapshot = func() error {
		r.mu.RLock()
		store := r.stateStore
		r.mu.RUnlock()
		if store == nil {
			return nil
		}
		current := result
		current.Error = sanitizePersistedError(current.Error)
		return store.Save(context.Background(), agentstate.Snapshot{Version: 1, Task: task, Result: current, Messages: append([]modelgateway.Message(nil), messages...), UpdatedAt: time.Now().UTC()})
	}
	if err = persistSnapshot(); err != nil {
		return
	}
	approved := make(map[string]bool, len(task.ApprovedTools))
	for _, n := range task.ApprovedTools {
		approved[n] = true
	}
	nativeTools := false
	if capability, ok := r.model.(interface {
		SupportsNativeToolCalling(context.Context, string) bool
	}); ok {
		nativeTools = capability.SupportsNativeToolCalling(runCtx, task.ModelID)
	}
	if nativeTools {
		messages[0].Content = r.systemPrompt(task.WorkspaceID, task.ApprovedTools, true)
		if err = persistSnapshot(); err != nil {
			return
		}
	}
	for step := 1; ; step++ {
		if e := runCtx.Err(); e != nil {
			err = e
			return
		}
		if e := guard.CheckStep(step); e != nil {
			err = e
			return
		}
		result.Steps = step
		modelMessages := contextmanager.Trim(messages, r.cfg.MaxContextChars)
		r.mu.Lock()
		if current, ok := r.tasks[task.ID]; ok {
			current.Steps = result.Steps
			current.ToolCalls = result.ToolCalls
			r.tasks[task.ID] = current
		}
		r.mu.Unlock()
		resp, e := r.model.Generate(runCtx, modelgateway.GenerateRequest{ModelID: task.ModelID, Messages: modelMessages, MaxOutputTokens: task.MaxOutputTokens, Tools: func() []modelgateway.ToolDefinition {
			if nativeTools {
				return r.toolDefinitions()
			}
			return nil
		}()})
		if e != nil {
			err = e
			return
		}
		if resp == nil {
			err = errors.New("model returned nil response")
			return
		}
		messages = append(messages, modelgateway.Message{Role: modelgateway.RoleAssistant, Content: resp.Content, ToolCalls: append([]modelgateway.ToolCall(nil), resp.ToolCalls...)})
		if e := persistSnapshot(); e != nil {
			err = e
			return
		}
		if len(resp.ToolCalls) > 0 {
			for _, call := range resp.ToolCalls {
				if e := call.Validate(); e != nil {
					err = e
					return
				}
				if e := guard.CheckToolCall(result.ToolCalls, call.Name, string(call.Arguments)); e != nil {
					err = e
					return
				}
				if _, e := r.tool.Registry().Get(call.Name); e != nil {
					err = e
					return
				}
				toolResult, invokeErr := r.invokeTool(runCtx, task.ID, call.ID, call.Name, call.Arguments, ws.Path, approved[call.Name])
				result.ToolCalls++
				content := ""
				if invokeErr != nil {
					b, _ := json.Marshal(map[string]string{"error": safeToolError(invokeErr)})
					content = string(b)
				} else {
					b, marshalErr := json.Marshal(toolResult)
					if marshalErr != nil {
						err = marshalErr
						return
					}
					content = string(b)
				}
				messages = append(messages, modelgateway.Message{Role: modelgateway.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: content})
				if e := persistSnapshot(); e != nil {
					err = e
					return
				}
			}
			continue
		}
		if nativeTools && len(resp.ToolCalls) == 0 {
			result.Content = strings.TrimSpace(resp.Content)
			if result.Content == "" {
				err = dom.ErrInvalidModelOutput
			}
			return
		}
		ctl, e := parseControl(resp.Content)
		if e != nil {
			err = e
			return
		}
		switch ctl.Type {
		case "final":
			result.Content = ctl.Content
			if strings.TrimSpace(result.Content) == "" {
				err = dom.ErrInvalidModelOutput
			}
			return
		case "tool_call":
			if strings.TrimSpace(ctl.Tool) == "" || len(ctl.Arguments) == 0 {
				err = dom.ErrInvalidModelOutput
				return
			}
			var args any
			if json.Unmarshal(ctl.Arguments, &args) != nil {
				err = dom.ErrInvalidModelOutput
				return
			}
			if e = guard.CheckToolCall(result.ToolCalls, ctl.Tool, args); e != nil {
				err = e
				return
			}
			_, e = r.tool.Registry().Get(ctl.Tool)
			if e != nil {
				err = e
				return
			}
			legacyCallID := stableLegacyCallID(task.ID, step, ctl.Tool, ctl.Arguments)
			toolResult, invokeErr := r.invokeTool(runCtx, task.ID, legacyCallID, ctl.Tool, ctl.Arguments, ws.Path, approved[ctl.Tool])
			result.ToolCalls++
			if invokeErr != nil {
				b, _ := json.Marshal(map[string]string{"error": safeToolError(invokeErr)})
				messages = append(messages, modelgateway.Message{Role: modelgateway.RoleUser, Name: "tool:" + ctl.Tool, Content: "Tool result for " + ctl.Tool + ": " + string(b)})
				if e := persistSnapshot(); e != nil {
					err = e
					return
				}
				continue
			}
			b, marshalErr := json.Marshal(toolResult)
			if marshalErr != nil {
				err = marshalErr
				return
			}
			messages = append(messages, modelgateway.Message{Role: modelgateway.RoleUser, Name: "tool:" + ctl.Tool, Content: "Tool result for " + ctl.Tool + ": " + string(b)})
			if e := persistSnapshot(); e != nil {
				err = e
				return
			}
		default:
			err = dom.ErrInvalidModelOutput
			return
		}
	}
}
func (r *Runtime) Cancel(id string) error {
	r.mu.RLock()
	cancel, ok := r.cancels[id]
	r.mu.RUnlock()
	if !ok {
		return dom.ErrTaskNotFound
	}
	cancel()
	return nil
}
func (r *Runtime) Get(id string) (dom.Result, error) {
	r.mu.RLock()
	v, ok := r.tasks[id]
	store := r.stateStore
	r.mu.RUnlock()
	if ok {
		return v, nil
	}
	if store != nil {
		snapshot, err := store.Load(context.Background(), id)
		if err == nil {
			return snapshot.Result, nil
		}
	}
	return dom.Result{}, dom.ErrTaskNotFound
}

func (r *Runtime) toolDefinitions() []modelgateway.ToolDefinition {
	specs := r.tool.Registry().List()
	out := make([]modelgateway.ToolDefinition, 0, len(specs))
	for _, spec := range specs {
		schema := map[string]any{"type": spec.Input.Type, "properties": spec.Input.Properties, "required": spec.Input.Required, "additionalProperties": spec.Input.AdditionalProperties}
		b, _ := json.Marshal(schema)
		out = append(out, modelgateway.ToolDefinition{Name: spec.Name, Description: spec.Description, Parameters: b})
	}
	return out
}

func sanitizePersistedError(value string) string {
	if len(value) > 2048 {
		return value[:2048]
	}
	return value
}
func (r *Runtime) GetSnapshot(ctx context.Context, id string) (agentstate.Snapshot, error) {
	r.mu.RLock()
	store := r.stateStore
	r.mu.RUnlock()
	if store == nil {
		return agentstate.Snapshot{}, agentstate.ErrNotFound
	}
	return store.Load(ctx, id)
}

func (r *Runtime) systemPrompt(workspace string, approved []string, native bool) string {
	specs := r.tool.Registry().List()
	b, _ := json.Marshal(specs)
	a, _ := json.Marshal(approved)
	protocol := "Return exactly one JSON object and no markdown. To call a tool: {\"type\":\"tool_call\",\"tool\":\"tool.name\",\"arguments\":{...}}. To finish: {\"type\":\"final\",\"content\":\"answer\"}."
	if native {
		protocol = "Use provider-native function calling for tools. When no tool is needed, return a normal assistant response containing the final answer."
	}
	return "You are an AgentMesh agent. You may use only the tools listed below. " + protocol + " Do not invent tool results. Read tools may be used normally. Write and execute tools may only be called when their exact names appear in approved_tools; otherwise do not attempt them. approved_tools: " + string(a) + ". Tools: " + string(b) + ". Workspace ID: " + workspace
}
func parseControl(s string) (control, error) {
	var c control
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimSpace(strings.TrimSuffix(s, "```"))
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSpace(strings.TrimSuffix(s, "```"))
	}
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return c, dom.ErrInvalidModelOutput
	}
	return c, nil
}
func safeToolError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "tool canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "tool timed out"
	default:
		return err.Error()
	}
}

func stableLegacyCallID(taskID string, step int, name string, args []byte) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s", taskID, step, name, args)))
	return hex.EncodeToString(sum[:])
}

func (r *Runtime) invokeTool(ctx context.Context, taskID, callID, name string, args json.RawMessage, workspacePath string, approved bool) (any, error) {
	r.mu.RLock()
	ledger := r.toolLedger
	r.mu.RUnlock()
	if ledger == nil {
		return r.tool.Invoke(ctx, name, args, workspacePath, approved)
	}
	keySum := sha256.Sum256([]byte(taskID + "\x00" + callID))
	key := hex.EncodeToString(keySum[:])
	started, err := ledger.Begin(ctx, key, taskID, callID, name)
	if err != nil {
		return nil, err
	}
	if !started {
		raw, storedError, completed, loadErr := ledger.Load(ctx, key)
		if loadErr != nil {
			return nil, loadErr
		}
		if !completed {
			return nil, errors.New("tool invocation is not safely replayable")
		}
		if storedError != "" {
			return nil, errors.New(storedError)
		}
		var value any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, fmt.Errorf("decode idempotent tool result: %w", err)
			}
		}
		return value, nil
	}
	value, invokeErr := r.tool.Invoke(ctx, name, args, workspacePath, approved)
	if completeErr := ledger.Complete(context.Background(), key, value, invokeErr); completeErr != nil {
		return nil, fmt.Errorf("persist tool invocation result: %w", completeErr)
	}
	return value, invokeErr
}
