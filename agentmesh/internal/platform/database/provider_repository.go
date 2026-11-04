package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/thkx/agentmesh/internal/db"
	"github.com/thkx/agentmesh/internal/domain/provider"
)

// RuntimeRepository persists provider instances and their model catalogue.
// Credentials are represented only by environment-variable references; raw secrets never enter SQLite.
type RuntimeRepository struct {
	queries *db.Queries
	db      *sql.DB
}

func NewRuntimeRepository(sqlDB *sql.DB) (*RuntimeRepository, error) {
	if sqlDB == nil {
		return nil, errors.New("runtime repository requires a database")
	}
	return &RuntimeRepository{queries: db.New(sqlDB), db: sqlDB}, nil
}

func (r *RuntimeRepository) Sync(ctx context.Context, providers []provider.Provider, models []provider.Model) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin runtime config transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := r.queries.WithTx(tx)
	now := time.Now().UTC()
	for _, p := range providers {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("validate provider %q: %w", p.ID, err)
		}
		createdAt := p.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		updatedAt := p.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = now
		}
		if err := q.UpsertProvider(ctx, db.UpsertProviderParams{
			ID: string(p.ID), Type: string(p.Type), Name: p.Name, Kind: string(p.Kind),
			BaseUrl: p.BaseURL, CredentialRef: p.CredentialRef, Enabled: boolInt(p.Enabled),
			CreatedAt: createdAt.UnixMilli(), UpdatedAt: updatedAt.UnixMilli(),
		}); err != nil {
			return fmt.Errorf("persist provider %q: %w", p.ID, err)
		}
	}
	for _, m := range models {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("validate model %q: %w", m.ID, err)
		}
		createdAt := m.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		updatedAt := m.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = now
		}
		if err := q.UpsertModel(ctx, db.UpsertModelParams{
			ID: m.ID, ProviderID: string(m.ProviderID), Name: m.Name, DisplayName: m.DisplayName,
			ContextWindow: int64(m.ContextWindow), MaxOutputTokens: int64(m.MaxOutputTokens),
			SupportsStreaming: boolInt(m.SupportsStreaming), Enabled: boolInt(m.Enabled),
			CreatedAt: createdAt.UnixMilli(), UpdatedAt: updatedAt.UnixMilli(),
		}); err != nil {
			return fmt.Errorf("persist model %q: %w", m.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit runtime config transaction: %w", err)
	}
	return nil
}

func (r *RuntimeRepository) ListProviders(ctx context.Context) ([]provider.Provider, error) {
	rows, err := r.queries.ListProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	out := make([]provider.Provider, 0, len(rows))
	for _, row := range rows {
		out = append(out, provider.Provider{
			ID: provider.ID(row.ID), Type: provider.Type(row.Type), Name: row.Name, Kind: provider.Kind(row.Kind),
			BaseURL: row.BaseUrl, CredentialRef: row.CredentialRef, Enabled: row.Enabled != 0,
			CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
		})
	}
	return out, nil
}

func (r *RuntimeRepository) ListModels(ctx context.Context) ([]provider.Model, error) {
	rows, err := r.queries.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	out := make([]provider.Model, 0, len(rows))
	for _, row := range rows {
		out = append(out, provider.Model{
			ID: row.ID, ProviderID: provider.ID(row.ProviderID), Name: row.Name, DisplayName: row.DisplayName,
			ContextWindow: int(row.ContextWindow), MaxOutputTokens: int(row.MaxOutputTokens),
			SupportsStreaming: row.SupportsStreaming != 0, Enabled: row.Enabled != 0,
			CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
		})
	}
	return out, nil
}

func (r *RuntimeRepository) DeleteProvider(ctx context.Context, id provider.ID) error {
	if err := r.queries.DeleteProvider(ctx, string(id)); err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	return nil
}

func (r *RuntimeRepository) DeleteModel(ctx context.Context, id string) error {
	if err := r.queries.DeleteModel(ctx, id); err != nil {
		return fmt.Errorf("delete model %q: %w", id, err)
	}
	return nil
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

var _ provider.CatalogRepository = (*RuntimeRepository)(nil)
