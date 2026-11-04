package tools

import (
	"context"
	"encoding/json"
	"errors"
	dom "github.com/thkx/agentmesh/internal/domain/tool"
	"testing"
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
	got, err := e.Invoke(context.Background(), "write", json.RawMessage(`{"v":"x"}`), "/tmp/ws", true)
	if err != nil || got != "ok" {
		t.Fatalf("invoke: %v %v", got, err)
	}
	if len(a.events) != 2 || !a.events[1].Allowed || !a.events[1].Approved {
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
