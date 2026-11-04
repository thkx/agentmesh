package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrProviderNotFound        = errors.New("provider not found")
	ErrProviderAlreadyExists   = errors.New("provider already registered")
	ErrProviderDisabled        = errors.New("provider is disabled")
	ErrModelNotFoundInRegistry = errors.New("model not found")
)

// ProviderRegistry stores provider instances, not provider vendor types.
type ProviderRegistry struct {
	mu    sync.RWMutex
	items map[ID]Provider
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{items: make(map[ID]Provider)}
}

func (r *ProviderRegistry) Register(p Provider) error {
	if err := p.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[p.ID]; ok {
		return fmt.Errorf("%w: %s", ErrProviderAlreadyExists, p.ID)
	}
	r.items[p.ID] = p
	return nil
}

func (r *ProviderRegistry) Upsert(p Provider) error {
	if err := p.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[p.ID] = p
	return nil
}

func (r *ProviderRegistry) Get(id ID) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.items[id]
	if !ok {
		return Provider{}, fmt.Errorf("%w: %s", ErrProviderNotFound, id)
	}
	return p, nil
}

func (r *ProviderRegistry) List() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.items))
	for _, p := range r.items {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ModelRegistry owns the internal model IDs exposed to callers.
type ModelRegistry struct {
	mu    sync.RWMutex
	items map[string]Model
}

func NewModelRegistry() *ModelRegistry { return &ModelRegistry{items: make(map[string]Model)} }
func (r *ModelRegistry) Register(m Model) error {
	if err := m.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[m.ID]; ok {
		return fmt.Errorf("%w: %s", ErrModelAlreadyExists, m.ID)
	}
	r.items[m.ID] = m
	return nil
}
func (r *ModelRegistry) Upsert(m Model) error {
	if err := m.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[m.ID] = m
	return nil
}
func (r *ModelRegistry) Resolve(ctx context.Context, id string) (Model, error) {
	if err := ctx.Err(); err != nil {
		return Model{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.items[id]
	if !ok {
		return Model{}, fmt.Errorf("%w: %s", ErrModelNotFoundInRegistry, id)
	}
	return m, nil
}
func (r *ModelRegistry) Get(id string) (Model, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.items[id]
	return m, ok
}
func (r *ModelRegistry) List() []Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Model, 0, len(r.items))
	for _, m := range r.items {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Delete removes a provider instance from the in-memory registry.
func (r *ProviderRegistry) Delete(id ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[id]; !ok {
		return fmt.Errorf("%w: %s", ErrProviderNotFound, id)
	}
	delete(r.items, id)
	return nil
}

// Delete removes a model from the in-memory registry.
func (r *ModelRegistry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[id]; !ok {
		return fmt.Errorf("%w: %s", ErrModelNotFoundInRegistry, id)
	}
	delete(r.items, id)
	return nil
}
