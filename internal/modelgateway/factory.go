package modelgateway

import (
	"errors"
	"fmt"
	"sync"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

var (
	ErrFactoryAlreadyRegistered = errors.New("adapter factory already registered")
	ErrFactoryNotRegistered     = errors.New("adapter factory not registered")
)

// AdapterBuilder creates a fresh adapter for one provider instance.
type AdapterBuilder func(provider.Provider) (Model, error)

type AdapterFactory struct {
	mu       sync.RWMutex
	builders map[provider.Type]AdapterBuilder
}

func NewAdapterFactory() *AdapterFactory {
	return &AdapterFactory{builders: make(map[provider.Type]AdapterBuilder)}
}
func (f *AdapterFactory) Register(kind provider.Type, builder AdapterBuilder) error {
	if kind == "" || builder == nil {
		return errors.New("provider type and adapter builder are required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.builders[kind]; ok {
		return fmt.Errorf("%w: %s", ErrFactoryAlreadyRegistered, kind)
	}
	f.builders[kind] = builder
	return nil
}
func (f *AdapterFactory) Build(p provider.Provider) (Model, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	f.mu.RLock()
	builder, ok := f.builders[p.Type]
	f.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFactoryNotRegistered, p.Type)
	}
	adapter, err := builder(p)
	if err != nil {
		return nil, fmt.Errorf("build adapter for provider %s: %w", p.ID, err)
	}
	if adapter == nil {
		return nil, ErrNilAdapter
	}
	return adapter, nil
}
