package credential

import (
	"context"
	"testing"

	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
)

// Compile-time assertions document the intended public contracts.
var (
	_ Store             = storeContract(nil)
	_ EnvironmentLookup = environmentContract(nil)
	_ Resolver          = resolverContract(nil)
	_ Manager           = managerContract(nil)
)

type storeContract func(context.Context, provider.ID, domaincredential.Secret) error

func (storeContract) Put(context.Context, provider.ID, domaincredential.Secret) error { return nil }
func (storeContract) Get(context.Context, provider.ID) (domaincredential.Secret, error) {
	return domaincredential.Secret{}, nil
}
func (storeContract) Delete(context.Context, provider.ID) error         { return nil }
func (storeContract) Exists(context.Context, provider.ID) (bool, error) { return false, nil }

type environmentContract func(string) (string, bool)

func (e environmentContract) Lookup(name string) (string, bool) { return e(name) }

type resolverContract func(context.Context, provider.Provider) (domaincredential.Credential, error)

func (resolverContract) Resolve(context.Context, provider.Provider) (domaincredential.Credential, error) {
	return domaincredential.Credential{}, nil
}
func (resolverContract) Status(context.Context, provider.Provider) (domaincredential.Record, error) {
	return domaincredential.Record{}, nil
}

type managerContract func(context.Context, provider.ID, domaincredential.Secret) error

func (managerContract) Set(context.Context, provider.ID, domaincredential.Secret) error { return nil }
func (managerContract) Delete(context.Context, provider.ID) error                       { return nil }
func (managerContract) Status(context.Context, provider.ID) (domaincredential.Record, error) {
	return domaincredential.Record{}, nil
}

func TestInterfacesAreDeclared(t *testing.T) {}
