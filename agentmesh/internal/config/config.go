package config

import "time"

type Config struct {
	App       AppConfig        `koanf:"app"`
	Server    ServerConfig     `koanf:"server"`
	Database  DatabaseConfig   `koanf:"database"`
	Log       LogConfig        `koanf:"log"`
	Providers []ProviderConfig `koanf:"providers"`
	Models    []ModelConfig    `koanf:"models"`
	Agent     AgentConfig      `koanf:"agent"`
	Scheduler SchedulerConfig  `koanf:"scheduler"`
}

type AppConfig struct {
	Name        string `koanf:"name"`
	Environment string `koanf:"environment"`
}

type ServerConfig struct {
	Host            string        `koanf:"host"`
	Port            int           `koanf:"port"`
	ReadTimeout     time.Duration `koanf:"read_timeout"`
	WriteTimeout    time.Duration `koanf:"write_timeout"`
	ShutdownTimeout time.Duration `koanf:"shutdown_timeout"`
}

type DatabaseConfig struct {
	Driver          string        `koanf:"driver"`
	DSN             string        `koanf:"dsn"`
	MaxOpenConns    int           `koanf:"max_open_conns"`
	MaxIdleConns    int           `koanf:"max_idle_conns"`
	ConnMaxLifetime time.Duration `koanf:"conn_max_lifetime"`
	BusyTimeout     time.Duration `koanf:"busy_timeout"`
}

type LogConfig struct {
	Level  string `koanf:"level"`
	Format string `koanf:"format"`
}

// ProviderConfig describes one independently configured provider instance.
type ProviderConfig struct {
	ID        string `koanf:"id"`
	Name      string `koanf:"name"`
	Type      string `koanf:"type"`
	Kind      string `koanf:"kind"`
	BaseURL   string `koanf:"base_url"`
	APIKeyEnv string `koanf:"api_key_env"`
	Enabled   bool   `koanf:"enabled"`
}

// ModelConfig maps an AgentMesh model ID to a provider instance and upstream model name.
type ModelConfig struct {
	ID                string `koanf:"id"`
	ProviderID        string `koanf:"provider_id"`
	Name              string `koanf:"name"`
	DisplayName       string `koanf:"display_name"`
	ContextWindow     int    `koanf:"context_window"`
	MaxOutputTokens   int    `koanf:"max_output_tokens"`
	SupportsStreaming bool   `koanf:"supports_streaming"`
	Enabled           bool   `koanf:"enabled"`
}

// AgentConfig controls local coding-agent execution and workspace isolation.
type AgentConfig struct {
	WorkspaceRoot string             `koanf:"workspace_root"`
	CLI           CLIConfig          `koanf:"cli"`
	Tools         ToolConfig         `koanf:"tools"`
	Runtime       AgentRuntimeConfig `koanf:"runtime"`
}
type AgentRuntimeConfig struct {
	MaxSteps        int `koanf:"max_steps"`
	MaxToolCalls    int `koanf:"max_tool_calls"`
	MaxContextChars int `koanf:"max_context_chars"`
	MaxConcurrent   int `koanf:"max_concurrent"`
	TimeoutSeconds  int `koanf:"timeout_seconds"`
}
type ToolConfig struct {
	MaxFileBytes    int64         `koanf:"max_file_bytes"`
	MaxOutputBytes  int           `koanf:"max_output_bytes"`
	AllowedCommands []string      `koanf:"allowed_commands"`
	CommandTimeout  time.Duration `koanf:"command_timeout"`
}
type CLIConfig struct {
	Claude   string `koanf:"claude"`
	Codex    string `koanf:"codex"`
	Gemini   string `koanf:"gemini"`
	OpenCode string `koanf:"opencode"`
}

type SchedulerConfig struct {
	Workers       int           `koanf:"workers"`
	WorkerID      string        `koanf:"worker_id"`
	PollInterval  time.Duration `koanf:"poll_interval"`
	LeaseDuration time.Duration `koanf:"lease_duration"`
	MaxAttempts   int           `koanf:"max_attempts"`
}
