package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

func Validate(c Config) error {
	return ValidateWithAPIAuth(c, false)
}

// ValidateWithAPIAuth validates a configuration whose HTTP API is protected
// by a sufficiently strong Bearer token when bound to a non-loopback address.
func ValidateWithAPIAuth(c Config, apiAuthEnabled bool) error {
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

	host := strings.TrimSpace(c.Server.Host)
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) && !apiAuthEnabled {
		return fmt.Errorf("server.host must be a loopback address unless API token authentication is enabled")
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

	if strings.TrimSpace(c.Agent.WorkspaceRoot) == "" {
		return fmt.Errorf("agent.workspace_root is required")
	}
	seenCodingRoots := make(map[string]struct{}, len(c.Coding.AllowedRepositoryRoots))
	for i, root := range c.Coding.AllowedRepositoryRoots {
		root = strings.TrimSpace(root)
		if root == "" || strings.ContainsAny(root, "\r\n\x00") {
			return fmt.Errorf("coding.allowed_repository_roots[%d] is invalid", i)
		}
		if _, exists := seenCodingRoots[root]; exists {
			return fmt.Errorf("coding.allowed_repository_roots contains duplicate root %q", root)
		}
		seenCodingRoots[root] = struct{}{}
	}
	if len(c.Agent.Tools.SandboxImage) > 256 {
		return fmt.Errorf("agent.tools.sandbox_image is too long")
	}
	for i, command := range c.Agent.Tools.AllowedCommands {
		if strings.TrimSpace(command) == "" || strings.ContainsAny(command, "\r\n\x00") {
			return fmt.Errorf("agent.tools.allowed_commands[%d] is invalid", i)
		}
	}
	for i, domain := range c.Agent.Tools.AllowedHTTPDomains {
		domain = strings.TrimSpace(domain)
		if domain == "" || strings.Contains(domain, "://") || strings.ContainsAny(domain, "/\\:@?#") || strings.Contains(domain, "*") {
			return fmt.Errorf("agent.tools.allowed_http_domains[%d] must be a plain hostname", i)
		}
	}
	if c.Agent.Runtime.MaxSteps < 1 || c.Agent.Runtime.MaxSteps > 64 {
		return fmt.Errorf("agent.runtime.max_steps must be between 1 and 64")
	}
	if c.Agent.Runtime.MaxToolCalls < 1 || c.Agent.Runtime.MaxToolCalls > 32 {
		return fmt.Errorf("agent.runtime.max_tool_calls must be between 1 and 32")
	}
	if c.Agent.Runtime.MaxContextChars < 1000 || c.Agent.Runtime.MaxContextChars > 1000000 {
		return fmt.Errorf("agent.runtime.max_context_chars must be between 1000 and 1000000")
	}
	if c.Agent.Runtime.MaxContextTokens < 0 || c.Agent.Runtime.MaxContextTokens > 1000000 {
		return fmt.Errorf("agent.runtime.max_context_tokens must be between 0 and 1000000")
	}
	if c.Agent.Runtime.MaxConcurrent < 1 || c.Agent.Runtime.MaxConcurrent > 32 {
		return fmt.Errorf("agent.runtime.max_concurrent must be between 1 and 32")
	}
	if c.Agent.Runtime.TimeoutSeconds < 1 || c.Agent.Runtime.TimeoutSeconds > 3600 {
		return fmt.Errorf("agent.runtime.timeout_seconds must be between 1 and 3600")
	}

	if c.Scheduler.Workers < 0 || c.Scheduler.Workers > 32 {
		return fmt.Errorf("scheduler.workers must be between 0 and 32 (0 uses one worker)")
	}
	if c.Scheduler.PollInterval < 0 || c.Scheduler.LeaseDuration < 0 || c.Scheduler.MaxAttempts < 0 {
		return fmt.Errorf("scheduler durations and max_attempts cannot be negative")
	}
	if c.Scheduler.LeaseDuration > 0 && c.Scheduler.LeaseDuration < 3*time.Second {
		return fmt.Errorf("scheduler.lease_duration must be at least 3s")
	}
	if c.Scheduler.MaxAttempts > 20 {
		return fmt.Errorf("scheduler.max_attempts cannot exceed 20")
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

	providerIDs := make(map[string]struct{}, len(c.Providers))
	for i, p := range c.Providers {
		id := strings.TrimSpace(p.ID)
		if id == "" {
			return fmt.Errorf("providers[%d].id is required", i)
		}
		if _, exists := providerIDs[id]; exists {
			return fmt.Errorf("duplicate provider id %q", id)
		}
		providerIDs[id] = struct{}{}
		switch strings.ToLower(p.Type) {
		case "openai", "anthropic", "gemini", "ollama":
		default:
			return fmt.Errorf("providers[%d].type is unsupported", i)
		}
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("providers[%d].name is required", i)
		}
		if p.BaseURL != "" {
			u, err := url.ParseRequestURI(p.BaseURL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("providers[%d].base_url must be an absolute http(s) URL", i)
			}
		}
		if strings.EqualFold(p.Type, "ollama") && p.APIKeyEnv != "" {
			return fmt.Errorf("providers[%d].api_key_env is not used by Ollama", i)
		}
		if !strings.EqualFold(p.Type, "ollama") && strings.TrimSpace(p.APIKeyEnv) == "" {
			return fmt.Errorf("providers[%d].api_key_env is required for cloud providers", i)
		}
	}
	modelIDs := make(map[string]struct{}, len(c.Models))
	for i, m := range c.Models {
		if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.ProviderID) == "" {
			return fmt.Errorf("models[%d] requires id, provider_id and name", i)
		}
		if _, exists := modelIDs[m.ID]; exists {
			return fmt.Errorf("duplicate model id %q", m.ID)
		}
		modelIDs[m.ID] = struct{}{}
		if _, exists := providerIDs[m.ProviderID]; !exists {
			return fmt.Errorf("models[%d] references unknown provider %q", i, m.ProviderID)
		}
		if m.ContextWindow < 0 || m.MaxOutputTokens < 0 || (m.ContextWindow > 0 && m.MaxOutputTokens > m.ContextWindow) {
			return fmt.Errorf("models[%d] has invalid token limits", i)
		}
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
