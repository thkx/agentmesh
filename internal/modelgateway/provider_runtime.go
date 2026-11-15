package modelgateway

import (
	"errors"
	"fmt"
	"sync"

	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
)

var ErrNilProviderRuntimeDependency = errors.New("provider runtime dependency is nil")

// ProviderRuntime manages the live adapter bound to each provider instance.
// Build happens before publishing changes. Missing credentials publish an
// enabled-but-unconfigured provider and remove its stale adapter; other build
// failures leave the previously published provider and adapter untouched.
type ProviderRuntime struct {
	mu        sync.Mutex
	providers *provider.ProviderRegistry
	adapters  *Registry
	factory   *AdapterFactory
}

func NewProviderRuntime(providers *provider.ProviderRegistry, adapters *Registry, factory *AdapterFactory) (*ProviderRuntime, error) {
	if providers == nil || adapters == nil || factory == nil {
		return nil, ErrNilProviderRuntimeDependency
	}
	return &ProviderRuntime{providers: providers, adapters: adapters, factory: factory}, nil
}

// ApplyProvider creates or updates one provider instance and its adapter.
func (r *ProviderRuntime) ApplyProvider(p provider.Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applyProviderLocked(p)
}

// RefreshProvider updates an already registered provider. Unlike ApplyProvider,
// it never creates a provider, which prevents a credential mutation racing with
// provider deletion from resurrecting a removed provider instance.
func (r *ProviderRuntime) RefreshProvider(p provider.Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.providers.Get(p.ID); err != nil {
		return err
	}
	return r.applyProviderLocked(p)
}

func (r *ProviderRuntime) applyProviderLocked(p provider.Provider) error {
	if err := p.Validate(); err != nil {
		return err
	}
	var adapter Model
	var err error
	unconfigured := false
	if p.Enabled {
		adapter, err = r.factory.Build(p)
		if errors.Is(err, appcredential.ErrCredentialUnavailable) {
			unconfigured = true
			err = nil
		} else if err != nil {
			return err
		}
	}
	if err := r.providers.Upsert(p); err != nil {
		return fmt.Errorf("publish provider: %w", err)
	}
	if p.Enabled && !unconfigured {
		if err := r.adapters.Replace(p.ID, adapter); err != nil {
			return fmt.Errorf("publish adapter: %w", err)
		}
	} else {
		if err := r.adapters.Unregister(p.ID); err != nil && !errors.Is(err, ErrAdapterNotFound) {
			return err
		}
	}
	return nil
}

// RemoveProvider removes the provider instance and its adapter.
// Models referring to this provider remain registered but cannot be resolved
// until the provider is added again; callers may delete those models explicitly.
func (r *ProviderRuntime) RemoveProvider(id provider.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.providers.Delete(id); err != nil {
		return err
	}
	if err := r.adapters.Unregister(id); err != nil && !errors.Is(err, ErrAdapterNotFound) {
		return err
	}
	return nil
}
