package config

import "time"

func Defaults() Config {
	return Config{
		App: AppConfig{
			Name:        "agentmesh",
			Environment: "development",
		},
		Server: ServerConfig{
			Host:            "127.0.0.1",
			Port:            8080,
			ReadTimeout:     15 * time.Second,
			WriteTimeout:    30 * time.Second,
			ShutdownTimeout: 15 * time.Second,
		},
		Database: DatabaseConfig{
			Driver:          "sqlite",
			DSN:             "./data/agentmesh.db",
			MaxOpenConns:    4,
			MaxIdleConns:    4,
			ConnMaxLifetime: 0,
			BusyTimeout:     5 * time.Second,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
		Providers: []ProviderConfig{},
		Models:    []ModelConfig{},
		Scheduler: SchedulerConfig{Workers: 1, PollInterval: 500 * time.Millisecond, LeaseDuration: 30 * time.Second, MaxAttempts: 3},
		Agent:     AgentConfig{WorkspaceRoot: "./data/workspaces", CLI: CLIConfig{Claude: "claude", Codex: "codex", Gemini: "gemini", OpenCode: "opencode"}, Runtime: AgentRuntimeConfig{MaxSteps: 12, MaxToolCalls: 8, MaxContextChars: 24000, MaxConcurrent: 4, TimeoutSeconds: 900}},
	}
}
