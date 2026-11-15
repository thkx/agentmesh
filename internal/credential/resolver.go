package credential

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
)

// ErrCredentialUnavailable indicates that a provider has no usable credential.
// It is deliberately distinct from storage and decryption failures.
var ErrCredentialUnavailable = errors.New("provider credential is not configured")

type OSEnvironment struct{}

func (OSEnvironment) Lookup(name string) (string, bool) {
	if strings.TrimSpace(name) == "" {
		return "", false
	}
	return os.LookupEnv(name)
}

type DefaultResolver struct {
	store Store
	env   EnvironmentLookup
}

func NewResolver(store Store, env EnvironmentLookup) (*DefaultResolver, error) {
	if store == nil || env == nil {
		return nil, ErrNilDependency
	}
	return &DefaultResolver{store: store, env: env}, nil
}

func (r *DefaultResolver) Resolve(ctx context.Context, p provider.Provider) (domaincredential.Credential, error) {
	if err := ctx.Err(); err != nil {
		return domaincredential.Credential{}, err
	}
	if p.CredentialRef != "" {
		if value, ok := r.env.Lookup(p.CredentialRef); ok && strings.TrimSpace(value) != "" {
			secret, err := domaincredential.NewSecret(strings.TrimSpace(value))
			if err != nil {
				return domaincredential.Credential{}, err
			}
			return domaincredential.Credential{ProviderID: p.ID, Source: domaincredential.SourceEnvironment, Secret: secret}, nil
		}
	}
	secret, err := r.store.Get(ctx, p.ID)
	if err != nil {
		if errors.Is(err, domaincredential.ErrNotFound) {
			return domaincredential.Credential{}, ErrCredentialUnavailable
		}
		return domaincredential.Credential{}, err
	}
	return domaincredential.Credential{ProviderID: p.ID, Source: domaincredential.SourceStored, Secret: secret, UpdatedAt: time.Now().UTC()}, nil
}

func (r *DefaultResolver) Status(ctx context.Context, p provider.Provider) (domaincredential.Record, error) {
	if err := ctx.Err(); err != nil {
		return domaincredential.Record{}, err
	}
	record := domaincredential.Record{ProviderID: p.ID, Status: domaincredential.StatusUnconfigured}
	if p.CredentialRef != "" {
		if value, ok := r.env.Lookup(p.CredentialRef); ok && strings.TrimSpace(value) != "" {
			record.Source = domaincredential.SourceEnvironment
			record.Status = domaincredential.StatusConfigured
			return record, nil
		}
	}
	exists, err := r.store.Exists(ctx, p.ID)
	if err != nil {
		return record, err
	}
	if exists {
		record.Source = domaincredential.SourceStored
		record.Status = domaincredential.StatusConfigured
	}
	return record, nil
}

var _ Resolver = (*DefaultResolver)(nil)
