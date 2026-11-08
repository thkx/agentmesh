package yamlcodec

import (
	"github.com/thkx/agentmesh/internal/domain/workflow"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	d := workflow.Definition{ID: "wf", Name: "demo", Version: 1, Entry: "start", Nodes: []workflow.Node{{ID: "start", Type: workflow.NodeStart}, {ID: "end", Type: workflow.NodeEnd}}, Edges: []workflow.Edge{{From: "start", To: "end"}}}
	b, e := Export(d)
	if e != nil {
		t.Fatal(e)
	}
	got, e := Import(b)
	if e != nil {
		t.Fatal(e)
	}
	if got.ID != d.ID || len(got.Edges) != 1 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestImportRejectsUnknownTopLevelField(t *testing.T) {
	data := []byte("id: demo\nname: demo\nversion: 1\nentry: start\nnodes:\n  - id: start\n    type: start\n  - id: end\n    type: end\nedges:\n  - from: start\n    to: end\nunknown: true\n")
	if _, err := Import(data); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}
