package config

import "testing"

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
