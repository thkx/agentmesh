package credential

import (
	"context"
	"errors"
	"fmt"
	"sync"

	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
)

type RuntimeUpdater interface{ RefreshProvider(provider.Provider) error }

type DefaultManager struct {
	mu        sync.Mutex
	store     Store
	resolver  Resolver
	providers *provider.ProviderRegistry
	runtime   RuntimeUpdater
}

func NewManager(store Store, resolver Resolver, providers *provider.ProviderRegistry, runtime RuntimeUpdater) (*DefaultManager, error) {
	if store == nil || resolver == nil || providers == nil || runtime == nil {
		return nil, ErrNilDependency
	}
	return &DefaultManager{store: store, resolver: resolver, providers: providers, runtime: runtime}, nil
}

func (m *DefaultManager) Set(ctx context.Context, id provider.ID, secret domaincredential.Secret) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if secret.Empty() {
		return domaincredential.ErrEmptySecret
	}
	p, err := m.providers.Get(id)
	if err != nil {
		return ErrProviderNotFound
	}
	previous, previousErr := m.store.Get(ctx, id)
	hadPrevious := previousErr == nil
	if previousErr != nil && !errors.Is(previousErr, domaincredential.ErrNotFound) {
		return fmt.Errorf("read previous credential: %w", previousErr)
	}
	if err := m.store.Put(ctx, id, secret); err != nil {
		return err
	}
	if err := m.runtime.RefreshProvider(p); err != nil {
		var rollback error
		if hadPrevious {
			rollback = m.store.Put(ctx, id, previous)
		} else {
			rollback = m.store.Delete(ctx, id)
		}
		return errors.Join(fmt.Errorf("apply provider credential: %w", err), rollback)
	}
	return nil
}

func (m *DefaultManager) Delete(ctx context.Context, id provider.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := m.providers.Get(id)
	if err != nil {
		return ErrProviderNotFound
	}
	previous, err := m.store.Get(ctx, id)
	if errors.Is(err, domaincredential.ErrNotFound) {
		return domaincredential.ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := m.store.Delete(ctx, id); err != nil {
		return err
	}
	if err := m.runtime.RefreshProvider(p); err != nil {
		return errors.Join(fmt.Errorf("apply provider after credential deletion: %w", err), m.store.Put(ctx, id, previous))
	}
	return nil
}

func (m *DefaultManager) Status(ctx context.Context, id provider.ID) (domaincredential.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.providers.Get(id)
	if err != nil {
		return domaincredential.Record{}, ErrProviderNotFound
	}
	return m.resolver.Status(ctx, p)
}

var _ Manager = (*DefaultManager)(nil)
