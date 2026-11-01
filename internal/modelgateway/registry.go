package modelgateway

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

var (
	ErrNilAdapter           = errors.New("model adapter is nil")
	ErrAdapterAlreadyExists = errors.New("model adapter already registered")
	ErrAdapterNotFound      = errors.New("model adapter not found")
)

// Model is the provider-specific model execution interface.
//
// Each provider adapter implements this interface.
// The adapter is responsible for translating AgentMesh requests
// into the provider's SDK request and response formats.
type Model interface {
	Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)

	Stream(ctx context.Context, req GenerateRequest) (<-chan StreamEvent, error)
}

type Registry struct {
	mu       sync.RWMutex
	adapters map[provider.ID]Model
}

func NewRegistry() *Registry {
	return &Registry{
		adapters: make(map[provider.ID]Model),
	}
}

func (r *Registry) Register(providerID provider.ID, adapter Model) error {
	if providerID == "" {
		return provider.ErrInvalidProviderID
	}

	if adapter == nil {
		return ErrNilAdapter
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.adapters[providerID]; exists {
		return fmt.Errorf(
			"%w: %s",
			ErrAdapterAlreadyExists,
			providerID,
		)
	}

	r.adapters[providerID] = adapter

	return nil
}

func (r *Registry) Get(providerID provider.ID) (Model, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	adapter, exists := r.adapters[providerID]
	if !exists {
		return nil, fmt.Errorf(
			"%w: %s",
			ErrAdapterNotFound,
			providerID,
		)
	}

	return adapter, nil
}

func (r *Registry) Has(providerID provider.ID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.adapters[providerID]

	return exists
}

func (r *Registry) Providers() []provider.ID {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]provider.ID, 0, len(r.adapters))

	for id := range r.adapters {
		result = append(result, id)
	}

	return result
}

// Replace atomically publishes a new adapter for future requests. Get returns an
// interface value holding the old adapter, so requests already in flight continue
// using that adapter safely. Adapters must not be closed on replacement unless a
// separate reference-counted retirement mechanism is introduced.
func (r *Registry) Replace(providerID provider.ID, adapter Model) error {
	if providerID == "" {
		return provider.ErrInvalidProviderID
	}
	if adapter == nil {
		return ErrNilAdapter
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[providerID] = adapter
	return nil
}

// Unregister removes one provider-instance adapter.
func (r *Registry) Unregister(providerID provider.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.adapters[providerID]; !ok {
		return fmt.Errorf("%w: %s", ErrAdapterNotFound, providerID)
	}
	delete(r.adapters, providerID)
	return nil
}
