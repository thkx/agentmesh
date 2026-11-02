package compiler

import (
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
	"testing"
)

func validDefinition() wf.Definition {
	return wf.Definition{ID: "demo", Name: "demo", Version: 1, Entry: "start", Nodes: []wf.Node{{ID: "start", Type: wf.NodeCondition, Condition: &wf.ConditionConfig{InputKey: "approved", Equals: true}}, {ID: "yes", Type: wf.NodeEnd}, {ID: "no", Type: wf.NodeEnd}}, Edges: []wf.Edge{{From: "start", To: "yes", Handle: "true"}, {From: "start", To: "no", Handle: "false"}}}
}
func TestCompileValidGraph(t *testing.T) {
	if _, e := Compile(validDefinition()); e != nil {
		t.Fatal(e)
	}
}
func TestCompileRejectsCycle(t *testing.T) {
	d := validDefinition()
	d.Edges = append(d.Edges, wf.Edge{From: "yes", To: "start"})
	if _, e := Compile(d); e == nil {
		t.Fatal("expected cycle error")
	}
}
func TestCompileRejectsUnreachable(t *testing.T) {
	d := validDefinition()
	d.Nodes = append(d.Nodes, wf.Node{ID: "lost", Type: wf.NodeEnd})
	if _, e := Compile(d); e == nil {
		t.Fatal("expected unreachable error")
	}
}

func TestCompilePureStartNode(t *testing.T) {
	d := wf.Definition{ID: "start-node", Name: "start-node", Version: 1, Entry: "start", Nodes: []wf.Node{{ID: "start", Type: wf.NodeStart}, {ID: "left", Type: wf.NodeEnd}, {ID: "right", Type: wf.NodeEnd}}, Edges: []wf.Edge{{From: "start", To: "left"}, {From: "start", To: "right"}}}
	if _, err := Compile(d); err != nil {
		t.Fatalf("expected pure start node with fan-out to compile: %v", err)
	}
}

func TestCompileStartNodeMustBeEntry(t *testing.T) {
	d := wf.Definition{ID: "bad-start", Name: "bad-start", Version: 1, Entry: "end", Nodes: []wf.Node{{ID: "start", Type: wf.NodeStart}, {ID: "end", Type: wf.NodeEnd}}, Edges: []wf.Edge{{From: "start", To: "end"}}}
	if _, err := Compile(d); err == nil {
		t.Fatal("expected non-entry start node to be rejected")
	}
}
