package modelgateway

import (
	"context"
	"errors"
	"testing"

	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
)

type fakeAdapter struct{}

func (fakeAdapter) Generate(context.Context, GenerateRequest) (*GenerateResponse, error) {
	return &GenerateResponse{}, nil
}
func (fakeAdapter) Stream(context.Context, GenerateRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}

func TestAdapterFactoryCreatesPerInstanceAdapters(t *testing.T) {
	f := NewAdapterFactory()
	calls := 0
	if err := f.Register(provider.TypeOpenAI, func(p provider.Provider) (Model, error) { calls++; return fakeAdapter{}, nil }); err != nil {
		t.Fatal(err)
	}
	for _, id := range []provider.ID{"openai-primary", "openai-backup"} {
		p := provider.Provider{ID: id, Type: provider.TypeOpenAI, Name: string(id), Kind: provider.KindCloud, Enabled: true}
		if _, err := f.Build(p); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("expected one adapter per instance, got %d", calls)
	}
}
func TestAdapterFactoryRejectsDuplicateBuilder(t *testing.T) {
	f := NewAdapterFactory()
	b := func(provider.Provider) (Model, error) { return fakeAdapter{}, nil }
	_ = f.Register(provider.TypeOpenAI, b)
	if err := f.Register(provider.TypeOpenAI, b); !errors.Is(err, ErrFactoryAlreadyRegistered) {
		t.Fatalf("expected duplicate factory error, got %v", err)
	}
}

func TestProviderRuntimeAppliesUpdatesAndRemovals(t *testing.T) {
	providers := provider.NewProviderRegistry()
	adapters := NewRegistry()
	factory := NewAdapterFactory()
	if err := factory.Register(provider.TypeOpenAI, func(provider.Provider) (Model, error) { return fakeAdapter{}, nil }); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewProviderRuntime(providers, adapters, factory)
	if err != nil {
		t.Fatal(err)
	}
	p := provider.Provider{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "Primary", Kind: provider.KindCloud, Enabled: true}
	if err := runtime.ApplyProvider(p); err != nil {
		t.Fatal(err)
	}
	if !adapters.Has(p.ID) {
		t.Fatal("adapter not registered")
	}
	p.Name = "Primary Updated"
	if err := runtime.ApplyProvider(p); err != nil {
		t.Fatal(err)
	}
	got, err := providers.Get(p.ID)
	if err != nil || got.Name != p.Name {
		t.Fatalf("provider update failed: %+v %v", got, err)
	}
	if err := runtime.RemoveProvider(p.ID); err != nil {
		t.Fatal(err)
	}
	if adapters.Has(p.ID) {
		t.Fatal("adapter still registered after removal")
	}
	if _, err := providers.Get(p.ID); !errors.Is(err, provider.ErrProviderNotFound) {
		t.Fatalf("expected provider removal, got %v", err)
	}
}

type isolatedAdapter struct {
	providerID provider.ID
	baseURL    string
	credential string
}

func (a *isolatedAdapter) Generate(context.Context, GenerateRequest) (*GenerateResponse, error) {
	return &GenerateResponse{Content: string(a.providerID)}, nil
}
func (a *isolatedAdapter) Stream(context.Context, GenerateRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 1)
	ch <- NewCompletedEvent(&GenerateResponse{Content: string(a.providerID)})
	close(ch)
	return ch, nil
}

func TestProviderRuntimeKeepsSameTypeInstancesIsolated(t *testing.T) {
	providers := provider.NewProviderRegistry()
	adapters := NewRegistry()
	factory := NewAdapterFactory()
	if err := factory.Register(provider.TypeOpenAI, func(p provider.Provider) (Model, error) {
		return &isolatedAdapter{providerID: p.ID, baseURL: p.BaseURL, credential: p.CredentialRef}, nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewProviderRuntime(providers, adapters, factory)
	if err != nil {
		t.Fatal(err)
	}
	instances := []provider.Provider{
		{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "Primary", Kind: provider.KindCloud, BaseURL: "https://primary.example/v1", CredentialRef: "PRIMARY_KEY", Enabled: true},
		{ID: "openai-backup", Type: provider.TypeOpenAI, Name: "Backup", Kind: provider.KindCloud, BaseURL: "https://backup.example/v1", CredentialRef: "BACKUP_KEY", Enabled: true},
	}
	for _, p := range instances {
		if err := runtime.ApplyProvider(p); err != nil {
			t.Fatalf("apply %s: %v", p.ID, err)
		}
	}
	for i, p := range instances {
		got, err := adapters.Get(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		adapter, ok := got.(*isolatedAdapter)
		if !ok {
			t.Fatalf("unexpected adapter type %T", got)
		}
		if adapter.providerID != p.ID || adapter.baseURL != p.BaseURL || adapter.credential != p.CredentialRef {
			t.Fatalf("instance %d received another instance's config: %+v", i, adapter)
		}
	}
}

func TestProviderRuntimeFailedBuildDoesNotReplaceWorkingAdapter(t *testing.T) {
	providers := provider.NewProviderRegistry()
	adapters := NewRegistry()
	factory := NewAdapterFactory()
	if err := factory.Register(provider.TypeOpenAI, func(p provider.Provider) (Model, error) {
		if p.BaseURL == "https://invalid.example/v1" {
			return nil, errors.New("invalid endpoint")
		}
		return &isolatedAdapter{providerID: p.ID, baseURL: p.BaseURL}, nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewProviderRuntime(providers, adapters, factory)
	if err != nil {
		t.Fatal(err)
	}
	original := provider.Provider{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "Primary", Kind: provider.KindCloud, BaseURL: "https://primary.example/v1", Enabled: true}
	if err := runtime.ApplyProvider(original); err != nil {
		t.Fatal(err)
	}
	updated := original
	updated.BaseURL = "https://invalid.example/v1"
	if err := runtime.ApplyProvider(updated); err == nil {
		t.Fatal("expected adapter build failure")
	}
	gotProvider, err := providers.Get(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotProvider.BaseURL != original.BaseURL {
		t.Fatalf("provider registry changed after failed build: %+v", gotProvider)
	}
	gotAdapter, err := adapters.Get(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotAdapter.(*isolatedAdapter).baseURL != original.BaseURL {
		t.Fatalf("working adapter was replaced: %+v", gotAdapter)
	}
}

func TestProviderRuntimeKeepsEnabledProviderWithoutCredential(t *testing.T) {
	providers := provider.NewProviderRegistry()
	adapters := NewRegistry()
	factory := NewAdapterFactory()
	if err := factory.Register(provider.TypeOpenAI, func(provider.Provider) (Model, error) {
		return nil, appcredential.ErrCredentialUnavailable
	}); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewProviderRuntime(providers, adapters, factory)
	if err != nil {
		t.Fatal(err)
	}
	p := provider.Provider{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "Primary", Kind: provider.KindCloud, Enabled: true}
	if err := runtime.ApplyProvider(p); err != nil {
		t.Fatalf("missing credential must not prevent provider registration: %v", err)
	}
	got, err := providers.Get(p.ID)
	if err != nil || !got.Enabled {
		t.Fatalf("provider was not registered as enabled: %+v %v", got, err)
	}
	if adapters.Has(p.ID) {
		t.Fatal("adapter must remain unavailable until credential is configured")
	}
}

func TestProviderRuntimeRemovesAdapterWhenCredentialBecomesUnavailable(t *testing.T) {
	providers := provider.NewProviderRegistry()
	adapters := NewRegistry()
	factory := NewAdapterFactory()
	configured := true
	if err := factory.Register(provider.TypeOpenAI, func(provider.Provider) (Model, error) {
		if !configured {
			return nil, appcredential.ErrCredentialUnavailable
		}
		return fakeAdapter{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewProviderRuntime(providers, adapters, factory)
	if err != nil {
		t.Fatal(err)
	}
	p := provider.Provider{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "Primary", Kind: provider.KindCloud, Enabled: true}
	if err := runtime.ApplyProvider(p); err != nil {
		t.Fatal(err)
	}
	if !adapters.Has(p.ID) {
		t.Fatal("expected configured adapter")
	}
	configured = false
	if err := runtime.ApplyProvider(p); err != nil {
		t.Fatal(err)
	}
	if adapters.Has(p.ID) {
		t.Fatal("stale adapter remained after credential became unavailable")
	}
}

func TestProviderRuntimeRefreshDoesNotResurrectDeletedProvider(t *testing.T) {
	providers := provider.NewProviderRegistry()
	adapters := NewRegistry()
	factory := NewAdapterFactory()
	if err := factory.Register(provider.TypeOpenAI, func(provider.Provider) (Model, error) { return fakeAdapter{}, nil }); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewProviderRuntime(providers, adapters, factory)
	if err != nil {
		t.Fatal(err)
	}
	p := provider.Provider{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "Primary", Kind: provider.KindCloud, Enabled: true}
	if err := runtime.ApplyProvider(p); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RemoveProvider(p.ID); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RefreshProvider(p); !errors.Is(err, provider.ErrProviderNotFound) {
		t.Fatalf("expected missing provider, got %v", err)
	}
	if _, err := providers.Get(p.ID); !errors.Is(err, provider.ErrProviderNotFound) {
		t.Fatal("refresh resurrected deleted provider")
	}
}
