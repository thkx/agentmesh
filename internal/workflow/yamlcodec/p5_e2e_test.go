package yamlcodec

import (
	"reflect"
	"strings"
	"testing"

	"github.com/thkx/agentmesh/internal/domain/workflow"
)

func TestP5TwoNodeAgentWorkflowYAMLAndGraphRoundTrip(t *testing.T) {
	original := workflow.Definition{
		ID: "p5-two-agent",
		Name: "P5 two-agent integration",
		Version: 3,
		Entry: "draft",
		Nodes: []workflow.Node{
			{ID: "draft", Type: workflow.NodeAgent, Name: "Draft", Agent: &workflow.AgentConfig{
				ModelID: "model-test", WorkspaceID: "workspace-test", Prompt: "Create a concise draft",
			}},
			{ID: "review", Type: workflow.NodeAgent, Name: "Review", Agent: &workflow.AgentConfig{
				ModelID: "model-test", WorkspaceID: "workspace-test", Prompt: "Review the previous node output",
			}},
			{ID: "finish", Type: workflow.NodeEnd, Name: "Finish"},
		},
		Edges: []workflow.Edge{
			{From: "draft", To: "review"},
			{From: "review", To: "finish"},
		},
	}

	yamlBytes, err := Export(original)
	if err != nil {
		t.Fatalf("export YAML: %v", err)
	}
	fromYAML, err := Import(yamlBytes)
	if err != nil {
		t.Fatalf("import YAML: %v", err)
	}
	if !reflect.DeepEqual(fromYAML, original) {
		t.Fatalf("YAML round trip changed workflow
want: %#v
got:  %#v", original, fromYAML)
	}

	graph, err := ToGraph(fromYAML)
	if err != nil {
		t.Fatalf("convert to graph: %v", err)
	}
	fromGraph, err := FromGraph(graph)
	if err != nil {
		t.Fatalf("convert from graph: %v", err)
	}
	if !reflect.DeepEqual(fromGraph, original) {
		t.Fatalf("graph round trip changed workflow
want: %#v
got:  %#v", original, fromGraph)
	}
}

func TestP5YAMLImportRejectsMultipleDocuments(t *testing.T) {
	data := []byte("id: wf\nname: wf\nversion: 1\nentry: start\nnodes:\n  - id: start\n    type: start\n  - id: end\n    type: end\nedges:\n  - from: start\n    to: end\n---\nid: second\n")
	if _, err := Import(data); err == nil {
		t.Fatal("multiple YAML documents must be rejected")
	}
}

func TestP5YAMLImportRejectsUnknownNestedField(t *testing.T) {
	data := []byte("id: wf\nname: wf\nversion: 1\nentry: start\nnodes:\n  - id: start\n    type: start\n    unexpected: true\n  - id: end\n    type: end\nedges:\n  - from: start\n    to: end\n")
	if _, err := Import(data); err == nil || !strings.Contains(err.Error(), "unknown node field") {
		t.Fatalf("expected unknown node field error, got %v", err)
	}
}

func TestP5GraphImportRejectsDanglingEdge(t *testing.T) {
	graph := Graph{
		ID: "wf", Name: "wf", Version: 1, Entry: "start",
		Nodes: []GraphNode{{ID: "start", Type: workflow.NodeStart}, {ID: "end", Type: workflow.NodeEnd}},
		Edges: []GraphEdge{{ID: "bad", Source: "start", Target: "missing"}},
	}
	if _, err := FromGraph(graph); err == nil {
		t.Fatal("graph with dangling edge must be rejected")
	}
}
