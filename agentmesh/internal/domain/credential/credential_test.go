package credential

import (
	"errors"
	"fmt"
	"testing"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

func TestNewSecretRejectsEmptyValue(t *testing.T) {
	if _, err := NewSecret("  \n"); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("expected ErrEmptySecret, got %v", err)
	}
}

func TestSecretFormattingRedactsValue(t *testing.T) {
	secret, err := NewSecret("sk-sensitive-value")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%v", secret); got != "[REDACTED]" {
		t.Fatalf("String leaked secret: %q", got)
	}
	if got := fmt.Sprintf("%#v", secret); got != "[REDACTED]" {
		t.Fatalf("GoString leaked secret: %q", got)
	}
	if got := secret.Reveal(); got != "sk-sensitive-value" {
		t.Fatalf("Reveal() = %q", got)
	}
}

func TestCredentialValidate(t *testing.T) {
	secret, _ := NewSecret("secret")
	valid := Credential{ProviderID: provider.ID("openai-primary"), Source: SourceStored, Secret: secret}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid credential rejected: %v", err)
	}
	invalid := valid
	invalid.Source = "unknown"
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource, got %v", err)
	}
}

func TestRecordNeverContainsSecretField(t *testing.T) {
	record := Record{ProviderID: provider.ID("openai-primary"), Source: SourceStored, Status: StatusConfigured}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
}
