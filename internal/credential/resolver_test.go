package credential

import (
	"context"
	"errors"
	"testing"

	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
)

type memoryStore struct {
	values map[provider.ID]domaincredential.Secret
}

func (m *memoryStore) Put(_ context.Context, id provider.ID, s domaincredential.Secret) error {
	if m.values == nil {
		m.values = map[provider.ID]domaincredential.Secret{}
	}
	m.values[id] = s
	return nil
}
func (m *memoryStore) Get(_ context.Context, id provider.ID) (domaincredential.Secret, error) {
	s, ok := m.values[id]
	if !ok {
		return domaincredential.Secret{}, domaincredential.ErrNotFound
	}
	return s, nil
}
func (m *memoryStore) Delete(_ context.Context, id provider.ID) error {
	delete(m.values, id)
	return nil
}
func (m *memoryStore) Exists(_ context.Context, id provider.ID) (bool, error) {
	_, ok := m.values[id]
	return ok, nil
}

type mapEnv map[string]string

func (m mapEnv) Lookup(k string) (string, bool) { v, ok := m[k]; return v, ok }

func TestResolverEnvironmentTakesPrecedenceOverStored(t *testing.T) {
	store := &memoryStore{}
	stored, _ := domaincredential.NewSecret("stored-key")
	_ = store.Put(context.Background(), "openai-primary", stored)
	resolver, _ := NewResolver(store, mapEnv{"PRIMARY_KEY": "environment-key"})
	p := provider.Provider{ID: "openai-primary", CredentialRef: "PRIMARY_KEY"}
	got, err := resolver.Resolve(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Secret.Reveal() != "environment-key" || got.Source != domaincredential.SourceEnvironment {
		t.Fatalf("wrong effective credential: source=%s", got.Source)
	}
}

func TestResolverFallsBackToStoredWhenEnvironmentMissingOrEmpty(t *testing.T) {
	store := &memoryStore{}
	stored, _ := domaincredential.NewSecret("stored-key")
	_ = store.Put(context.Background(), "openai-primary", stored)
	resolver, _ := NewResolver(store, mapEnv{"PRIMARY_KEY": "   "})
	got, err := resolver.Resolve(context.Background(), provider.Provider{ID: "openai-primary", CredentialRef: "PRIMARY_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Secret.Reveal() != "stored-key" || got.Source != domaincredential.SourceStored {
		t.Fatalf("expected stored fallback, got %s", got.Source)
	}
}

func TestResolverReportsUnconfiguredWithoutFailingStartup(t *testing.T) {
	resolver, _ := NewResolver(&memoryStore{}, mapEnv{})
	p := provider.Provider{ID: "openai-primary", CredentialRef: "PRIMARY_KEY"}
	if _, err := resolver.Resolve(context.Background(), p); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("expected unconfigured error, got %v", err)
	}
	record, err := resolver.Status(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != domaincredential.StatusUnconfigured {
		t.Fatalf("unexpected status: %s", record.Status)
	}
}

func TestResolverPropagatesStorageErrors(t *testing.T) {
	resolver, _ := NewResolver(errorStore{}, mapEnv{})
	_, err := resolver.Resolve(context.Background(), provider.Provider{ID: "openai-primary"})
	if err == nil || errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("expected storage failure, got %v", err)
	}
}

type errorStore struct{}

func (errorStore) Put(context.Context, provider.ID, domaincredential.Secret) error {
	return errors.New("storage unavailable")
}
func (errorStore) Get(context.Context, provider.ID) (domaincredential.Secret, error) {
	return domaincredential.Secret{}, errors.New("storage unavailable")
}
func (errorStore) Delete(context.Context, provider.ID) error { return nil }
func (errorStore) Exists(context.Context, provider.ID) (bool, error) {
	return false, errors.New("storage unavailable")
}

func TestResolverKeepsSameTypeProviderInstancesIsolated(t *testing.T) {
	resolver, _ := NewResolver(&memoryStore{}, mapEnv{
		"PRIMARY_KEY": "primary-secret",
		"BACKUP_KEY":  "backup-secret",
	})
	primary, err := resolver.Resolve(context.Background(), provider.Provider{ID: "openai-primary", CredentialRef: "PRIMARY_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := resolver.Resolve(context.Background(), provider.Provider{ID: "openai-backup", CredentialRef: "BACKUP_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if primary.Secret.Reveal() != "primary-secret" || backup.Secret.Reveal() != "backup-secret" {
		t.Fatal("provider instances received each other's credentials")
	}
}
