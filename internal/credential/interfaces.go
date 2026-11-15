// Package credential defines application-level credential management
// contracts. Implementations belong in platform packages.
package credential

import (
	"context"

	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
)

// Store persists provider credentials. Implementations MUST encrypt secrets
// before writing them to durable storage and MUST never log plaintext values.
type Store interface {
	Put(ctx context.Context, providerID provider.ID, secret domaincredential.Secret) error
	Get(ctx context.Context, providerID provider.ID) (domaincredential.Secret, error)
	Delete(ctx context.Context, providerID provider.ID) error
	Exists(ctx context.Context, providerID provider.ID) (bool, error)
}

// EnvironmentLookup abstracts process environment access for deterministic tests.
type EnvironmentLookup interface {
	Lookup(name string) (value string, ok bool)
}

// Resolver resolves the effective credential for one provider instance.
// The planned precedence is Environment > Stored. A missing credential is not
// an application startup error; it is reported as StatusUnconfigured.
type Resolver interface {
	Resolve(ctx context.Context, p provider.Provider) (domaincredential.Credential, error)
	Status(ctx context.Context, p provider.Provider) (domaincredential.Record, error)
}

// Manager is the use-case boundary for changing stored credentials at runtime.
type Manager interface {
	Set(ctx context.Context, providerID provider.ID, secret domaincredential.Secret) error
	Delete(ctx context.Context, providerID provider.ID) error
	Status(ctx context.Context, providerID provider.ID) (domaincredential.Record, error)
}
