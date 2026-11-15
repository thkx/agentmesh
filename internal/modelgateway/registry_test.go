package modelgateway

import (
	"context"
	"testing"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

type namedAdapter string

func (a namedAdapter) Generate(context.Context, GenerateRequest) (*GenerateResponse, error) {
	return &GenerateResponse{Content: string(a)}, nil
}
func (a namedAdapter) Stream(context.Context, GenerateRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}

func TestRegistryReplacePreservesInFlightAdapterReference(t *testing.T) {
	r := NewRegistry()
	id := provider.ID("openai-primary")
	old := namedAdapter("old")
	if err := r.Register(id, old); err != nil {
		t.Fatal(err)
	}
	// Gateway requests obtain an adapter pointer before executing it. Replacing
	// the map entry affects new requests but does not invalidate this reference.
	inFlight, err := r.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Replace(id, namedAdapter("new")); err != nil {
		t.Fatal(err)
	}
	newRequest, err := r.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	oldResponse, err := inFlight.Generate(context.Background(), GenerateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	newResponse, err := newRequest.Generate(context.Background(), GenerateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if oldResponse.Content != "old" || newResponse.Content != "new" {
		t.Fatalf("unexpected adapter generations: old=%q new=%q", oldResponse.Content, newResponse.Content)
	}
}
