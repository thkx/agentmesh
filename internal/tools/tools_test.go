package tools

import (
	"context"
	"encoding/json"
	"errors"
	dom "github.com/thkx/agentmesh/internal/domain/tool"
	"testing"
	"time"
)

type testPolicy struct {
	approval bool
	allow    bool
}

func (p testPolicy) Authorize(context.Context, dom.Spec, json.RawMessage, string) Decision {
	return Decision{Allowed: p.allow, RequiresApproval: p.approval}
}

type testAudit struct{ events []AuditEvent }

func (a *testAudit) Record(_ context.Context, e AuditEvent) error {
	a.events = append(a.events, e)
	return nil
}
func TestEngineApprovalAndAudit(t *testing.T) {
	r := NewRegistry()
	err := r.Register(Tool{Spec: dom.Spec{Name: "write", Description: "write", Permission: dom.PermissionWrite, Input: dom.Schema{Type: "object", Properties: map[string]dom.Property{"v": {Type: "string"}}, Required: []string{"v"}}}, Handler: HandlerFunc(func(context.Context, json.RawMessage) (any, error) { return "ok", nil })})
	if err != nil {
		t.Fatal(err)
	}
	a := &testAudit{}
	e := NewEngine(r, testPolicy{approval: true}, a)
	if _, err = e.Invoke(context.Background(), "write", json.RawMessage(`{"v":"x"}`), "/tmp/ws", false); !errors.Is(err, dom.ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if _, err = e.Invoke(context.Background(), "write", json.RawMessage(`{"v":"x"}`), "/tmp/ws", true); !errors.Is(err, dom.ErrApprovalRequired) {
		t.Fatalf("client boolean bypassed approval: %v", err)
	}
	store := &testApprovalStore{items: make(map[string]testApproval)}
	e.SetApprovalStore(store)
	token, _, err := e.IssueApproval(context.Background(), "write", json.RawMessage(`{"v":"x"}`), "/tmp/ws", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.InvokeWithApproval(context.Background(), "write", json.RawMessage(`{"v":"x"}`), "/tmp/ws", token)
	if err != nil || got != "ok" {
		t.Fatalf("invoke: %v %v", got, err)
	}
	if _, err = e.InvokeWithApproval(context.Background(), "write", json.RawMessage(`{"v":"x"}`), "/tmp/ws", token); !errors.Is(err, dom.ErrApprovalRequired) {
		t.Fatalf("approval replay accepted: %v", err)
	}
	if len(a.events) != 4 || !a.events[2].Allowed || !a.events[2].Approved || a.events[2].Authorization != "human_approval" {
		t.Fatalf("unexpected audit: %#v", a.events)
	}
}
func TestEngineRejectsUnknownArgument(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(Tool{Spec: dom.Spec{Name: "x", Description: "x", Permission: dom.PermissionRead, Input: dom.Schema{Type: "object", Properties: map[string]dom.Property{}, AdditionalProperties: false}}, Handler: HandlerFunc(func(context.Context, json.RawMessage) (any, error) { return nil, nil })})
	e := NewEngine(r, testPolicy{allow: true}, nil)
	if _, err := e.Invoke(context.Background(), "x", json.RawMessage(`{"unexpected":1}`), "/tmp", false); !errors.Is(err, dom.ErrInvalidArguments) {
		t.Fatalf("got %v", err)
	}
}

type testApproval struct {
	tool, workspace, args string
	expires               time.Time
	used                  bool
}
type testApprovalStore struct{ items map[string]testApproval }

func (s *testApprovalStore) CreateApproval(_ context.Context, hash, tool, workspace, args string, expires time.Time) error {
	s.items[hash] = testApproval{tool: tool, workspace: workspace, args: args, expires: expires}
	return nil
}
func (s *testApprovalStore) ConsumeApproval(_ context.Context, hash, tool, workspace, args string, now time.Time) (bool, error) {
	a, ok := s.items[hash]
	if !ok || a.used || !now.Before(a.expires) || a.tool != tool || a.workspace != workspace || a.args != args {
		return false, nil
	}
	a.used = true
	s.items[hash] = a
	return true, nil
}

func TestAgentInvocationEnforcesApprovedToolsServerSide(t *testing.T) {
	r := NewRegistry()
	called := false
	_ = r.Register(Tool{Spec: dom.Spec{Name: "write", Description: "write", Permission: dom.PermissionWrite, Input: dom.Schema{Type: "object", Properties: map[string]dom.Property{}, AdditionalProperties: true}}, Handler: HandlerFunc(func(context.Context, json.RawMessage) (any, error) { called = true; return "ok", nil })})
	e := NewEngine(r, testPolicy{allow: true}, nil)
	ctx := WithAgentInvocation(context.Background())
	if _, err := e.Invoke(ctx, "write", json.RawMessage(`{}`), "/tmp/ws", true); !errors.Is(err, dom.ErrDenied) {
		t.Fatalf("unlisted tool was not denied: %v", err)
	}
	ctx = WithAgentToolGrant(ctx, "write")
	if _, err := e.Invoke(ctx, "write", json.RawMessage(`{}`), "/tmp/ws", false); err != nil {
		t.Fatalf("server grant was not honored: %v", err)
	}
	if !called {
		t.Fatal("tool handler was not called")
	}
}
