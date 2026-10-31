package modelgateway

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

var (
	ErrModelNotFound      = errors.New("model not found")
	ErrModelAlreadyExists = errors.New("model already registered")
	ErrNilModelResolver   = errors.New("model resolver is nil")
)

// Resolver resolves an internal AgentMesh model ID
// into a provider model configuration.
type Resolver interface {
	Resolve(ctx context.Context, modelID string) (provider.Model, error)
}

// StaticResolver is an in-memory resolver.
//
// It is suitable for bootstrap configuration and tests.
// A database-backed resolver can replace it later.
type StaticResolver struct {
	mu     sync.RWMutex
	models map[string]provider.Model
}

func NewStaticResolver(models ...provider.Model) (*StaticResolver, error) {
	resolver := &StaticResolver{
		models: make(map[string]provider.Model),
	}

	for _, model := range models {
		if err := resolver.Register(model); err != nil {
			return nil, err
		}
	}

	return resolver, nil
}

func (r *StaticResolver) Register(model provider.Model) error {
	if err := model.Validate(); err != nil {
		return fmt.Errorf("invalid model: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.models[model.ID]; exists {
		return fmt.Errorf(
			"%w: %s",
			ErrModelAlreadyExists,
			model.ID,
		)
	}

	r.models[model.ID] = model

	return nil
}

func (r *StaticResolver) Resolve(ctx context.Context, modelID string) (provider.Model, error) {
	if err := ctx.Err(); err != nil {
		return provider.Model{}, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	model, exists := r.models[modelID]
	if !exists {
		return provider.Model{}, fmt.Errorf(
			"%w: %s",
			ErrModelNotFound,
			modelID,
		)
	}

	return model, nil
}
