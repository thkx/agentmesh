package runtime

import (
	"context"
	"encoding/json"
	"errors"
	agentstate "github.com/thkx/agentmesh/internal/agent/state"
	"github.com/thkx/agentmesh/internal/workspace"
	"path/filepath"
	"testing"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
	toolDomain "github.com/thkx/agentmesh/internal/domain/tool"
	modelgateway "github.com/thkx/agentmesh/internal/modelgateway"
	"github.com/thkx/agentmesh/internal/tools"
)

type fakeModel struct {
	outputs []string
	calls   int
}

func (m *fakeModel) Generate(_ context.Context, _ modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	if m.calls >= len(m.outputs) {
		return nil, errors.New("no fake response")
	}
	s := m.outputs[m.calls]
	m.calls++
	return &modelgateway.GenerateResponse{Content: s}, nil
}

func newTestRuntime(t *testing.T, outputs ...string) (*Runtime, *tools.Registry) {
	t.Helper()
	reg := tools.NewRegistry()
	spec := toolDomain.Spec{Name: "test.echo", Description: "echo", Permission: toolDomain.PermissionRead, Input: toolDomain.Schema{Type: "object", Properties: map[string]toolDomain.Property{"value": {Type: "string"}}, Required: []string{"value"}}}
	if err := reg.Register(tools.Tool{Spec: spec, Handler: tools.HandlerFunc(func(_ context.Context, b json.RawMessage) (any, error) {
		var v map[string]string
		_ = json.Unmarshal(b, &v)
		return map[string]string{"echo": v["value"]}, nil
	})}); err != nil {
		t.Fatal(err)
	}
	engine := tools.NewEngine(reg, allowRead{}, nil)
	wm, err := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wm.Create(context.Background(), "ws-1"); err != nil {
		t.Fatal(err)
	}
	rt, err := New(&fakeModel{outputs: outputs}, engine, wm, Config{DefaultMaxSteps: 5, DefaultMaxToolCalls: 3, MaxContextChars: 1000, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	return rt, reg
}

type allowRead struct{}

func (allowRead) Authorize(_ context.Context, s toolDomain.Spec, _ json.RawMessage, _ string) tools.Decision {
	return tools.Decision{Allowed: s.Permission == toolDomain.PermissionRead}
}

func TestRuntimeToolCallThenFinal(t *testing.T) {
	rt, _ := newTestRuntime(t, `{"type":"tool_call","tool":"test.echo","arguments":{"value":"hello"}}`, `{"type":"final","content":"finished"}`)
	got, err := rt.Run(context.Background(), dom.Task{ID: "task-1", ModelID: "model-1", WorkspaceID: "ws-1", Prompt: "say hello"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != dom.StatusSucceeded || got.Content != "finished" || got.ToolCalls != 1 || got.Steps != 2 {
		t.Fatalf("unexpected result: %+v", got)
	}
}
func TestRuntimeRejectsInvalidModelControl(t *testing.T) {
	rt, _ := newTestRuntime(t, "not json")
	_, err := rt.Run(context.Background(), dom.Task{ID: "task-1", ModelID: "model-1", WorkspaceID: "ws-1", Prompt: "do it"})
	if !errors.Is(err, dom.ErrInvalidModelOutput) {
		t.Fatalf("expected invalid output, got %v", err)
	}
}
func TestRuntimeRequiresValidTask(t *testing.T) {
	rt, _ := newTestRuntime(t)
	_, err := rt.Run(context.Background(), dom.Task{})
	if !errors.Is(err, dom.ErrInvalidTask) {
		t.Fatalf("expected invalid task, got %v", err)
	}
}
func TestRuntimeTaskIDCannotBeReused(t *testing.T) {
	rt, _ := newTestRuntime(t, `{"type":"final","content":"ok"}`)
	task := dom.Task{ID: "same", ModelID: "m", WorkspaceID: "ws-1", Prompt: "p"}
	if _, err := rt.Run(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Run(context.Background(), task); err == nil {
		t.Fatal("expected duplicate task rejection")
	}
}

type blockingModel struct{ started chan struct{} }

func (m blockingModel) Generate(ctx context.Context, _ modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	close(m.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRuntimeCancel(t *testing.T) {
	reg := tools.NewRegistry()
	engine := tools.NewEngine(reg, allowRead{}, nil)
	wm, err := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wm.Create(context.Background(), "ws-1"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	rt, err := New(blockingModel{started: started}, engine, wm, Config{DefaultMaxSteps: 2, DefaultMaxToolCalls: 2, MaxContextChars: 1000, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan dom.Result, 1)
	go func() {
		result, _ := rt.Run(context.Background(), dom.Task{ID: "cancel-me", ModelID: "m", WorkspaceID: "ws-1", Prompt: "wait"})
		done <- result
	}()
	<-started
	if err := rt.Cancel("cancel-me"); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.Status != dom.StatusCanceled {
		t.Fatalf("expected canceled, got %+v", got)
	}
}

type nativeFakeModel struct{ calls int }

func (m *nativeFakeModel) SupportsNativeToolCalling(context.Context, string) bool { return true }
func (m *nativeFakeModel) Generate(_ context.Context, req modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	m.calls++
	if m.calls == 1 {
		if len(req.Tools) != 1 {
			return nil, errors.New("native tool schema was not supplied")
		}
		return &modelgateway.GenerateResponse{ToolCalls: []modelgateway.ToolCall{{ID: "call-1", Name: "test.echo", Arguments: json.RawMessage(`{"value":"native"}`)}}, FinishReason: modelgateway.FinishReasonToolCall}, nil
	}
	if len(req.Messages) < 4 || req.Messages[len(req.Messages)-1].Role != modelgateway.RoleTool {
		return nil, errors.New("native tool result was not appended")
	}
	return &modelgateway.GenerateResponse{Content: "native finished"}, nil
}
func TestRuntimeNativeToolCallThenFinal(t *testing.T) {
	rt, _ := newTestRuntime(t, `unused`)
	rt.model = &nativeFakeModel{}
	got, err := rt.Run(context.Background(), dom.Task{ID: "native-task", ModelID: "model-1", WorkspaceID: "ws-1", Prompt: "echo", ApprovedTools: []string{"test.echo"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != dom.StatusSucceeded || got.Content != "native finished" || got.ToolCalls != 1 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

type memoryStateStore struct {
	snapshots map[string]agentstate.Snapshot
}

func (s *memoryStateStore) Save(_ context.Context, snapshot agentstate.Snapshot) error {
	if snapshot.Version == 0 {
		snapshot.Version = 1
	}
	if s.snapshots == nil {
		s.snapshots = map[string]agentstate.Snapshot{}
	}
	s.snapshots[snapshot.Task.ID] = snapshot
	return nil
}
func (s *memoryStateStore) Load(_ context.Context, id string) (agentstate.Snapshot, error) {
	value, ok := s.snapshots[id]
	if !ok {
		return agentstate.Snapshot{}, agentstate.ErrNotFound
	}
	return value, nil
}
func TestRuntimePersistsFullConversationAndCanLoadAfterCacheEviction(t *testing.T) {
	rt, _ := newTestRuntime(t, `{"type":"tool_call","tool":"test.echo","arguments":{"value":"persist"}}`, `{"type":"final","content":"saved"}`)
	store := &memoryStateStore{}
	rt.SetStateStore(store)
	task := dom.Task{ID: "persist-task", ModelID: "model-1", WorkspaceID: "ws-1", Prompt: "persist conversation"}
	if _, err := rt.Run(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	delete(rt.tasks, task.ID)
	result, err := rt.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "saved" {
		t.Fatalf("unexpected persisted result: %+v", result)
	}
	snapshot, err := rt.GetSnapshot(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) < 4 {
		t.Fatalf("expected full conversation transcript, got %d messages", len(snapshot.Messages))
	}
}

func TestRuntimeAllowsSafeRetryBeforeAnyToolInvocation(t *testing.T) {
	rt, _ := newTestRuntime(t)
	model := rt.model.(*fakeModel)
	task := dom.Task{ID: "safe-retry", ModelID: "model-1", WorkspaceID: "ws-1", Prompt: "retry me"}
	if _, err := rt.Run(context.Background(), task); err == nil {
		t.Fatal("expected first model attempt to fail")
	}
	model.outputs = []string{`{"type":"final","content":"recovered"}`}
	model.calls = 0
	got, err := rt.Run(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != dom.StatusSucceeded || got.Content != "recovered" {
		t.Fatalf("unexpected retry result: %+v", got)
	}
}

func TestRuntimeDoesNotRetryTaskAfterToolInvocation(t *testing.T) {
	rt, _ := newTestRuntime(t, `{"type":"tool_call","tool":"test.echo","arguments":{"value":"once"}}`, "invalid response")
	task := dom.Task{ID: "unsafe-retry", ModelID: "model-1", WorkspaceID: "ws-1", Prompt: "do tool"}
	if _, err := rt.Run(context.Background(), task); err == nil {
		t.Fatal("expected invalid model output")
	}
	if _, err := rt.Run(context.Background(), task); err == nil {
		t.Fatal("expected retry to be rejected after tool invocation")
	}
}
