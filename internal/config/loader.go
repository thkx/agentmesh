package config

import (
	"fmt"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

func Load(path string) (Config, error) {
	k := koanf.New(".")

	defaults := map[string]interface{}{
		"app.name":                        "agentmesh",
		"app.environment":                 "development",
		"server.host":                     "127.0.0.1",
		"server.port":                     8080,
		"server.read_timeout":             15 * time.Second,
		"server.write_timeout":            30 * time.Second,
		"server.shutdown_timeout":         15 * time.Second,
		"database.driver":                 "sqlite",
		"database.dsn":                    "./data/agentmesh.db",
		"database.max_open_conns":         4,
		"database.max_idle_conns":         4,
		"database.conn_max_lifetime":      time.Duration(0),
		"database.busy_timeout":           5 * time.Second,
		"log.level":                       "info",
		"log.format":                      "json",
		"providers":                       []map[string]interface{}{},
		"models":                          []map[string]interface{}{},
		"agent.workspace_root":            "./data/workspaces",
		"agent.runtime.max_steps":         12,
		"agent.runtime.max_tool_calls":    8,
		"agent.runtime.max_context_chars": 24000,
		"agent.runtime.max_concurrent":    4,
		"agent.runtime.timeout_seconds":   900,
		"agent.cli.claude":                "claude",
		"agent.cli.codex":                 "codex",
		"agent.cli.gemini":                "gemini",
		"agent.cli.opencode":              "opencode",
	}

	if err := k.Load(confmap.Provider(defaults, "."), nil); err != nil {
		return Config{}, fmt.Errorf("load defaults: %w", err)
	}

	if path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return Config{}, fmt.Errorf("load config %q: %w", path, err)
		}
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	if err := Validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}
