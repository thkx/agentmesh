package config

import (
	"fmt"
	"strings"
)

func Validate(c Config) error {
	if strings.TrimSpace(c.App.Name) == "" {
		return fmt.Errorf("app.name is required")
	}

	switch c.App.Environment {
	case "development", "test", "production":
	default:
		return fmt.Errorf("app.environment must be development, test or production")
	}

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}

	if c.Server.ReadTimeout <= 0 {
		return fmt.Errorf("server.read_timeout must be positive")
	}

	if c.Server.WriteTimeout <= 0 {
		return fmt.Errorf("server.write_timeout must be positive")
	}

	if c.Server.ShutdownTimeout <= 0 {
		return fmt.Errorf("server.shutdown_timeout must be positive")
	}

	if c.Database.Driver != "sqlite" {
		return fmt.Errorf("unsupported database.driver %q", c.Database.Driver)
	}

	if strings.TrimSpace(c.Database.DSN) == "" {
		return fmt.Errorf("database.dsn is required")
	}

	if c.Database.MaxOpenConns < 1 {
		return fmt.Errorf("database.max_open_conns must be positive")
	}

	if c.Database.MaxIdleConns < 0 ||
		c.Database.MaxIdleConns > c.Database.MaxOpenConns {
		return fmt.Errorf("database.max_idle_conns must be between 0 and max_open_conns")
	}

	if c.Database.BusyTimeout < 0 {
		return fmt.Errorf("database.busy_timeout cannot be negative")
	}

	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("unsupported log.level %q", c.Log.Level)
	}

	switch strings.ToLower(c.Log.Format) {
	case "json", "text":
	default:
		return fmt.Errorf("unsupported log.format %q", c.Log.Format)
	}

	return nil
}
