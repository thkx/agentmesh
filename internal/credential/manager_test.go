package credential

import (
	"context"
	"errors"
	"testing"

	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
)

type testRuntime struct {
	calls int
	err   error
}

func (r *testRuntime) RefreshProvider(provider.Provider) error { r.calls++; return r.err }

type statusResolver struct{ status domaincredential.Record }

func (r statusResolver) Resolve(context.Context, provider.Provider) (domaincredential.Credential, error) {
	return domaincredential.Credential{}, nil
}
func (r statusResolver) Status(context.Context, provider.Provider) (domaincredential.Record, error) {
	return r.status, nil
}

func TestManagerSetPersistsAndAppliesRuntime(t *testing.T) {
	store := &memoryStore{}
	providers := provider.NewProviderRegistry()
	p := provider.Provider{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "Primary", Kind: provider.KindCloud, Enabled: true}
	if err := providers.Register(p); err != nil {
		t.Fatal(err)
	}
	runtime := &testRuntime{}
	manager, err := NewManager(store, statusResolver{}, providers, runtime)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := domaincredential.NewSecret("do-not-leak")
	if err := manager.Set(context.Background(), p.ID, secret); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reveal() != "do-not-leak" || runtime.calls != 1 {
		t.Fatal("credential was not persisted and applied")
	}
}

func TestManagerSetRollsBackWhenRuntimeUpdateFails(t *testing.T) {
	store := &memoryStore{}
	providers := provider.NewProviderRegistry()
	p := provider.Provider{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "Primary", Kind: provider.KindCloud, Enabled: true}
	_ = providers.Register(p)
	old, _ := domaincredential.NewSecret("old-secret")
	_ = store.Put(context.Background(), p.ID, old)
	runtime := &testRuntime{err: errors.New("adapter build failed")}
	manager, _ := NewManager(store, statusResolver{}, providers, runtime)
	newSecret, _ := domaincredential.NewSecret("new-secret")
	if err := manager.Set(context.Background(), p.ID, newSecret); err == nil {
		t.Fatal("expected runtime error")
	}
	got, err := store.Get(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reveal() != "old-secret" {
		t.Fatal("previous credential was not restored")
	}
}

func TestManagerDeleteRestoresCredentialWhenRuntimeUpdateFails(t *testing.T) {
	store := &memoryStore{}
	providers := provider.NewProviderRegistry()
	p := provider.Provider{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "Primary", Kind: provider.KindCloud, Enabled: true}
	_ = providers.Register(p)
	secret, _ := domaincredential.NewSecret("stored-secret")
	_ = store.Put(context.Background(), p.ID, secret)
	runtime := &testRuntime{err: errors.New("adapter build failed")}
	manager, _ := NewManager(store, statusResolver{}, providers, runtime)
	if err := manager.Delete(context.Background(), p.ID); err == nil {
		t.Fatal("expected runtime error")
	}
	got, err := store.Get(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reveal() != "stored-secret" {
		t.Fatal("credential was not restored")
	}
}
