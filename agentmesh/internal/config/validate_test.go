package config

import (
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
