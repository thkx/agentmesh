package contextmanager

import (
	"github.com/thkx/agentmesh/internal/modelgateway"
	"testing"
)

func TestFitReservesOutputAndKeepsSystem(t *testing.T) {
	m := NewManager(nil)
	msgs := []modelgateway.Message{{Role: modelgateway.RoleSystem, Content: "system"}, {Role: modelgateway.RoleUser, Content: "old old old old"}, {Role: modelgateway.RoleUser, Content: "new"}}
	got, err := m.Fit(msgs, Budget{ModelWindow: 20, OutputReserve: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) == 0 || got.Messages[0].Role != modelgateway.RoleSystem {
		t.Fatalf("system instruction lost: %+v", got.Messages)
	}
}
