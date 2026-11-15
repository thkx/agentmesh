package credential

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

// Source identifies where a provider credential is obtained from.
type Source string

const (
	SourceEnvironment Source = "environment"
	SourceStored      Source = "stored"
)

// Status describes credential availability without exposing its value.
type Status string

const (
	StatusUnconfigured Status = "unconfigured"
	StatusConfigured   Status = "configured"
	StatusInvalid      Status = "invalid"
)

var (
	ErrInvalidProviderID = errors.New("credential provider id is required")
	ErrInvalidSource     = errors.New("invalid credential source")
	ErrEmptySecret       = errors.New("credential secret is empty")
	ErrNotFound          = errors.New("credential not found")
	ErrAlreadyExists     = errors.New("credential already exists")
)

// Secret keeps its value private and redacts common formatting operations.
// It is an in-memory value only; persistence implementations must encrypt it.
type Secret struct {
	value string
}

func NewSecret(value string) (Secret, error) {
	if strings.TrimSpace(value) == "" {
		return Secret{}, ErrEmptySecret
	}
	return Secret{value: value}, nil
}

// Reveal returns the secret for use by a provider adapter. Callers must not log it.
func (s Secret) Reveal() string { return s.value }

func (s Secret) Empty() bool { return strings.TrimSpace(s.value) == "" }

func (Secret) String() string   { return "[REDACTED]" }
func (Secret) GoString() string { return "[REDACTED]" }

// Credential is the resolved, in-memory credential for one provider instance.
type Credential struct {
	ProviderID provider.ID
	Source     Source
	Secret     Secret
	UpdatedAt  time.Time
}

func (c Credential) Validate() error {
	if strings.TrimSpace(string(c.ProviderID)) == "" {
		return ErrInvalidProviderID
	}
	if c.Source != SourceEnvironment && c.Source != SourceStored {
		return fmt.Errorf("%w: %q", ErrInvalidSource, c.Source)
	}
	if c.Secret.Empty() {
		return ErrEmptySecret
	}
	return nil
}

// Record contains safe metadata only. It never contains the credential value.
type Record struct {
	ProviderID provider.ID `json:"provider_id"`
	Source     Source      `json:"source"`
	Status     Status      `json:"status"`
	UpdatedAt  *time.Time  `json:"updated_at,omitempty"`
}

func (r Record) Validate() error {
	if strings.TrimSpace(string(r.ProviderID)) == "" {
		return ErrInvalidProviderID
	}
	if r.Source != "" && r.Source != SourceEnvironment && r.Source != SourceStored {
		return fmt.Errorf("%w: %q", ErrInvalidSource, r.Source)
	}
	switch r.Status {
	case StatusUnconfigured, StatusConfigured, StatusInvalid:
		return nil
	default:
		return fmt.Errorf("invalid credential status: %q", r.Status)
	}
}
