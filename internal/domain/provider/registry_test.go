package provider

import (
	"context"
	"errors"
	"testing"
)

func testProvider(id ID) Provider {
	return Provider{ID: id, Type: TypeOpenAI, Name: string(id), Kind: KindCloud, Enabled: true}
}
func TestProviderRegistryAllowsMultipleInstancesOfSameType(t *testing.T) {
	r := NewProviderRegistry()
	for _, id := range []ID{"openai-primary", "openai-backup"} {
		if err := r.Register(testProvider(id)); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.List(); len(got) != 2 {
		t.Fatalf("got %d providers", len(got))
	}
}
func TestProviderRegistryRejectsDuplicateInstanceID(t *testing.T) {
	r := NewProviderRegistry()
	if err := r.Register(testProvider("primary")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(testProvider("primary")); !errors.Is(err, ErrProviderAlreadyExists) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}
func TestModelRegistryResolveAndDuplicate(t *testing.T) {
	r := NewModelRegistry()
	m := Model{ID: "chat", ProviderID: "openai-primary", Name: "gpt-4.1", Enabled: true}
	if err := r.Register(m); err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(context.Background(), "chat")
	if err != nil || got.ProviderID != m.ProviderID {
		t.Fatalf("resolve got=%+v err=%v", got, err)
	}
	if err := r.Register(m); !errors.Is(err, ErrModelAlreadyExists) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}
