package config

import (
	"os"
	"testing"
	"time"
)

func TestDefaultsAreValid(t *testing.T) {
	if err := Validate(Defaults()); err != nil {
		t.Fatalf("expected defaults to be valid: %v", err)
	}
}

func TestValidateRejectsInvalidPort(t *testing.T) {
	cfg := Defaults()
	cfg.Server.Port = 70000

	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestValidateRejectsUnsupportedDatabase(t *testing.T) {
	cfg := Defaults()
	cfg.Database.Driver = "postgres"

	if err := Validate(cfg); err == nil {
		t.Fatal("expected unsupported database error")
	}
}

func TestValidateProviderInstancesAndModelReferences(t *testing.T) {
	cfg := Defaults()
	cfg.Providers = []ProviderConfig{
		{ID: "openai-primary", Name: "Primary", Type: "openai", Kind: "cloud", APIKeyEnv: "OPENAI_PRIMARY_KEY", Enabled: true},
		{ID: "openai-backup", Name: "Backup", Type: "openai", Kind: "cloud", APIKeyEnv: "OPENAI_BACKUP_KEY", Enabled: true},
	}
	cfg.Models = []ModelConfig{{ID: "chat", ProviderID: "openai-primary", Name: "gpt-4.1", Enabled: true}}
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid multi-instance config: %v", err)
	}
	cfg.Models[0].ProviderID = "missing"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected unknown provider reference to fail")
	}
}

func TestValidateRejectsNonLoopbackServerHost(t *testing.T) {
	cfg := Defaults()
	cfg.Server.Host = "0.0.0.0"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected non-loopback host to be rejected")
	}
}

func TestValidateAgentRuntimeLimits(t *testing.T) {
	cfg := Defaults()
	cfg.Agent.Runtime.MaxSteps = 65
	if err := Validate(cfg); err == nil {
		t.Fatal("expected max steps validation error")
	}
	cfg = Defaults()
	cfg.Agent.Runtime.MaxConcurrent = 0
	if err := Validate(cfg); err == nil {
		t.Fatal("expected max concurrent validation error")
	}
}

func TestValidateSchedulerLimits(t *testing.T) {
	cfg := Defaults()
	cfg.Scheduler.Workers = 33
	if err := Validate(cfg); err == nil {
		t.Fatal("expected scheduler worker limit error")
	}
	cfg = Defaults()
	cfg.Scheduler.LeaseDuration = 2 * time.Second
	if err := Validate(cfg); err == nil {
		t.Fatal("expected minimum scheduler lease error")
	}
	cfg = Defaults()
	cfg.Scheduler.MaxAttempts = 21
	if err := Validate(cfg); err == nil {
		t.Fatal("expected scheduler attempts limit error")
	}
}

func TestValidateWithAPIAuthAllowsRemoteHostOnlyWithAuth(t *testing.T) {
	cfg := Defaults()
	cfg.Server.Host = "0.0.0.0"
	if err := ValidateWithAPIAuth(cfg, false); err == nil {
		t.Fatal("expected remote listener without API auth to be rejected")
	}
	if err := ValidateWithAPIAuth(cfg, true); err != nil {
		t.Fatalf("authenticated remote listener should validate: %v", err)
	}
}


func TestResolveAPITokenFromSecureFile(t *testing.T) {
	t.Setenv("AGENTMESH_API_TOKEN", "")
	path := t.TempDir() + "/api-token"
	if err := os.WriteFile(path, []byte("01234567890123456789012345678901\n"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := ResolveAPIToken(path)
	if err != nil {
		t.Fatalf("resolve token: %v", err)
	}
	if token != "01234567890123456789012345678901" {
		t.Fatalf("unexpected token value")
	}
}

func TestResolveAPITokenRejectsInsecurePermissions(t *testing.T) {
	t.Setenv("AGENTMESH_API_TOKEN", "")
	path := t.TempDir() + "/api-token"
	if err := os.WriteFile(path, []byte("01234567890123456789012345678901"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveAPIToken(path); err == nil {
		t.Fatal("expected insecure token file permissions to be rejected")
	}
}

func TestResolveAPITokenEnvironmentOverridesFile(t *testing.T) {
	path := t.TempDir() + "/missing"
	t.Setenv("AGENTMESH_API_TOKEN", "environment-token-that-is-long-enough-123")
	token, err := ResolveAPIToken(path)
	if err != nil || token != "environment-token-that-is-long-enough-123" {
		t.Fatalf("environment token should override file: token=%q err=%v", token, err)
	}
}
