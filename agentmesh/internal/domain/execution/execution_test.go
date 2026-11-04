package execution

import (
	"errors"
	"testing"
)

func TestRequestValidate(t *testing.T) {
	base := Request{ID: "run-1", Provider: ProviderCodex, Prompt: "inspect repo", WorkspacePath: t.TempDir()}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	bad := base
	bad.Provider = "unknown"
	if !errors.Is(bad.Validate(), ErrUnsupportedProvider) {
		t.Fatal("expected unsupported provider")
	}
	bad = base
	bad.PermissionMode = "unrestricted"
	if !errors.Is(bad.Validate(), ErrInvalidRequest) {
		t.Fatal("expected invalid permission")
	}
	bad = base
	bad.ID = ""
	if !errors.Is(bad.Validate(), ErrInvalidRequest) {
		t.Fatal("expected invalid id")
	}
}
