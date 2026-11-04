package provider

import "context"

// CatalogRepository is the persistence boundary for provider instances and model definitions.
// Implementations belong to platform adapters; Gateway and domain registries do not depend on SQL.
type CatalogRepository interface {
	Sync(ctx context.Context, providers []Provider, models []Model) error
	ListProviders(ctx context.Context) ([]Provider, error)
	ListModels(ctx context.Context) ([]Model, error)
	DeleteProvider(ctx context.Context, id ID) error
	DeleteModel(ctx context.Context, id string) error
}
