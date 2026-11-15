// Package airesource implements the multi-provider AI resource pool and gateway.
package airesource

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai-compatible"
	ProtocolAnthropic Protocol = "anthropic"
	ProtocolGemini    Protocol = "gemini"
)

type Provider struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Protocol  Protocol  `json:"protocol"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Endpoint struct {
	ID         string    `json:"id"`
	ProviderID string    `json:"provider_id"`
	Name       string    `json:"name"`
	BaseURL    string    `json:"base_url"`
	Priority   int       `json:"priority"`
	TimeoutMS  int       `json:"timeout_ms"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
type CredentialInput struct {
	ID             string `json:"id"`
	ProviderID     string `json:"provider_id"`
	EndpointID     string `json:"endpoint_id"`
	Name           string `json:"name"`
	APIKey         string `json:"api_key"`
	Priority       int    `json:"priority"`
	Weight         int    `json:"weight"`
	MaxConcurrency int    `json:"max_concurrency"`
}
type Credential struct {
	ID             string    `json:"id"`
	ProviderID     string    `json:"provider_id"`
	EndpointID     string    `json:"endpoint_id"`
	Name           string    `json:"name"`
	MaskedKey      string    `json:"masked_key"`
	Status         string    `json:"status"`
	Priority       int       `json:"priority"`
	Weight         int       `json:"weight"`
	MaxConcurrency int       `json:"max_concurrency"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type Model struct {
	ID                string    `json:"id"`
	ModelKey          string    `json:"model_key"`
	DisplayName       string    `json:"display_name"`
	Enabled           bool      `json:"enabled"`
	SupportsStreaming bool      `json:"supports_streaming"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}
type BindingInput struct {
	CredentialID  string `json:"credential_id"`
	ModelID       string `json:"model_id"`
	UpstreamModel string `json:"upstream_model"`
	Enabled       *bool  `json:"enabled"`
}
type RoutingPolicy struct {
	ModelID          string    `json:"model_id"`
	Strategy         string    `json:"strategy"`
	MaxRetries       int       `json:"max_retries"`
	FailureThreshold int       `json:"failure_threshold"`
	CooldownSeconds  int       `json:"cooldown_seconds"`
	UpdatedAt        time.Time `json:"updated_at"`
}
type Health struct {
	CredentialID        string    `json:"credential_id"`
	State               string    `json:"state"`
	FailureCount        int       `json:"failure_count"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	CooldownUntil       time.Time `json:"cooldown_until,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
	LastStatusCode      int       `json:"last_status_code"`
	LastLatencyMS       int64     `json:"last_latency_ms"`
	LastSuccessAt       time.Time `json:"last_success_at,omitempty"`
	LastFailureAt       time.Time `json:"last_failure_at,omitempty"`
}
type Usage struct {
	Requests     int64 `json:"requests"`
	Successes    int64 `json:"successes"`
	Failures     int64 `json:"failures"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ToolCall struct {
	ID       string            `json:"id"`
	Type     string            `json:"type,omitempty"`
	Function ToolCallFunction  `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type GenerateRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature *float64  `json:"temperature,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
	Tools       []Tool    `json:"tools,omitempty"`
	ToolChoice string     `json:"tool_choice,omitempty"`
}
type GenerateResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []Choice    `json:"choices"`
	Usage   UsageTokens `json:"usage"`
}
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}
type UsageTokens struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
type Candidate struct {
	Credential    Credential
	Provider      Provider
	Endpoint      Endpoint
	Model         Model
	UpstreamModel string
	Secret        string
	Health        Health
}

var (
	ErrInvalid              = errors.New("invalid AI resource")
	ErrNotFound             = errors.New("AI resource not found")
	ErrNoCandidate          = errors.New("no healthy credential is available for model")
	ErrUnsupportedProtocol  = errors.New("unsupported provider protocol")
	ErrStreamingUnsupported = errors.New("streaming is not supported by this gateway adapter")
)

func validateID(v string) error {
	if strings.TrimSpace(v) == "" || len(v) > 128 {
		return fmt.Errorf("%w: id is required and must be at most 128 chars", ErrInvalid)
	}
	return nil
}
func (p Provider) Validate() error {
	if err := validateID(p.ID); err != nil {
		return err
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: provider name required", ErrInvalid)
	}
	switch p.Protocol {
	case ProtocolOpenAI, ProtocolAnthropic, ProtocolGemini:
	default:
		return fmt.Errorf("%w: protocol %q", ErrUnsupportedProtocol, p.Protocol)
	}
	return nil
}
func (e Endpoint) Validate() error {
	if err := validateID(e.ID); err != nil {
		return err
	}
	if err := validateID(e.ProviderID); err != nil {
		return err
	}
	u, err := url.ParseRequestURI(e.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%w: base_url must be an absolute http(s) URL without credentials, query, or fragment", ErrInvalid)
	}
	if e.TimeoutMS <= 0 {
		return fmt.Errorf("%w: timeout_ms must be positive", ErrInvalid)
	}
	return nil
}
func (c CredentialInput) Validate() error {
	if err := validateID(c.ID); err != nil {
		return err
	}
	if err := validateID(c.ProviderID); err != nil {
		return err
	}
	if err := validateID(c.EndpointID); err != nil {
		return err
	}
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.APIKey) == "" {
		return fmt.Errorf("%w: name and api_key are required", ErrInvalid)
	}
	if c.Weight <= 0 {
		return fmt.Errorf("%w: weight must be positive", ErrInvalid)
	}
	if c.MaxConcurrency <= 0 {
		return fmt.Errorf("%w: max_concurrency must be positive", ErrInvalid)
	}
	return nil
}
func (m Model) Validate() error {
	if err := validateID(m.ID); err != nil {
		return err
	}
	if strings.TrimSpace(m.ModelKey) == "" || strings.TrimSpace(m.DisplayName) == "" {
		return fmt.Errorf("%w: model_key and display_name are required", ErrInvalid)
	}
	return nil
}

type RequestLog struct {
	ID           string    `json:"id"`
	ModelID      string    `json:"model_id"`
	CredentialID string    `json:"credential_id"`
	ProviderID   string    `json:"provider_id"`
	EndpointID   string    `json:"endpoint_id"`
	Status       string    `json:"status"`
	StatusCode   int       `json:"status_code"`
	LatencyMS    int64     `json:"latency_ms"`
	ErrorCode    string    `json:"error_code,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
