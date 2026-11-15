package credential

import "errors"

var (
	ErrNilDependency       = errors.New("credential dependency is nil")
	ErrEnvironmentNotFound = errors.New("credential environment variable is not set")
	ErrProviderNotFound    = errors.New("provider not found for credential operation")
)
