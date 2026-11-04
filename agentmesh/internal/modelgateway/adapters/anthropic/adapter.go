package anthropicadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go/v2"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

const providerID provider.ID = provider.IDAnthropic

type Adapter struct {
	client     anthropic.Client
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func New(apiKey string, options ...anthropicoption.RequestOption) (*Adapter, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("anthropic api key is required")
	}

	opts := make([]anthropicoption.RequestOption, 0, len(options)+2)
	opts = append(opts, anthropicoption.WithoutEnvironmentDefaults())
	opts = append(opts, anthropicoption.WithAPIKey(apiKey))
	opts = append(opts, options...)

	return &Adapter{client: anthropic.NewClient(opts...), apiKey: apiKey, baseURL: "https://api.anthropic.com", httpClient: &http.Client{Timeout: 0, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func NewWithClient(client anthropic.Client) *Adapter {
	return &Adapter{client: client}
}

func NewWithBaseURL(apiKey, baseURL string, options ...anthropicoption.RequestOption) (*Adapter, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	opts := append([]anthropicoption.RequestOption{anthropicoption.WithBaseURL(baseURL)}, options...)
	adapter, err := New(apiKey, opts...)
	if err != nil {
		return nil, err
	}
	adapter.baseURL = baseURL
	return adapter, nil
}

func (a *Adapter) SupportsNativeToolCalling() bool {
	return strings.TrimSpace(a.apiKey) != "" && strings.TrimSpace(a.baseURL) != ""
}

var _ modelgateway.Model = (*Adapter)(nil)

func (a *Adapter) Generate(
	ctx context.Context,
	req modelgateway.GenerateRequest,
) (*modelgateway.GenerateResponse, error) {
	if len(req.Tools) > 0 && a.SupportsNativeToolCalling() {
		return a.generateNative(ctx, req)
	}
	params, err := buildRequest(req)
	if err != nil {
		return nil, err
	}

	message, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return nil, normalizeError(err)
	}

	if message == nil {
		return nil, errors.New("anthropic returned nil message")
	}

	return mapResponse(message, req), nil
}

func (a *Adapter) Stream(
	ctx context.Context,
	req modelgateway.GenerateRequest,
) (<-chan modelgateway.StreamEvent, error) {
	if len(req.Tools) > 0 {
		return nil, errors.New("Anthropic native tool streaming is not implemented")
	}
	params, err := buildRequest(req)
	if err != nil {
		return nil, err
	}

	stream := a.client.Messages.NewStreaming(ctx, params)
	if stream == nil {
		return nil, errors.New("anthropic returned nil stream")
	}

	out := make(chan modelgateway.StreamEvent)

	go func() {
		defer close(out)
		defer func() { _ = stream.Close() }()

		message := anthropic.Message{}

		for stream.Next() {
			if ctx.Err() != nil {
				return
			}

			event := stream.Current()

			if err := message.Accumulate(event); err != nil {
				send(ctx, out, modelgateway.NewErrorEvent(normalizeError(err)))
				return
			}

			switch e := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
				if delta, ok := e.Delta.AsAny().(anthropic.TextDelta); ok {
					if delta.Text != "" {
						if !send(ctx, out, modelgateway.NewTextDeltaEvent(
							providerID, req.ModelID, delta.Text,
						)) {
							return
						}
					}
				}

			case anthropic.MessageStopEvent:
				response := mapResponse(&message, req)

				usage := response.Usage
				if !send(ctx, out, modelgateway.NewUsageEvent(
					providerID, req.ModelID, usage,
				)) {
					return
				}

				send(ctx, out, modelgateway.NewCompletedEvent(response))
				return
			}
		}

		if err := stream.Err(); err != nil {
			send(ctx, out, modelgateway.NewErrorEvent(normalizeError(err)))
			return
		}

		send(ctx, out, modelgateway.NewErrorEvent(
			errors.New("anthropic stream ended without message_stop"),
		))
	}()

	return out, nil
}

func buildRequest(
	req modelgateway.GenerateRequest,
) (anthropic.MessageNewParams, error) {
	modelName := strings.TrimSpace(req.ProviderModel)
	if modelName == "" {
		return anthropic.MessageNewParams{}, errors.New("anthropic model is required")
	}

	messages := make([]anthropic.MessageParam, 0, len(req.Messages))
	system := make([]anthropic.TextBlockParam, 0, 2)

	if strings.TrimSpace(req.System) != "" {
		system = append(system, anthropic.TextBlockParam{
			Text: req.System,
		})
	}

	for _, message := range req.Messages {
		switch message.Role {
		case modelgateway.RoleSystem, modelgateway.RoleDeveloper:
			system = append(system, anthropic.TextBlockParam{
				Text: message.Content,
			})

		case modelgateway.RoleUser:
			messages = append(messages, anthropic.NewUserMessage(
				anthropic.NewTextBlock(message.Content),
			))

		case modelgateway.RoleAssistant:
			messages = append(messages, anthropic.NewAssistantMessage(
				anthropic.NewTextBlock(message.Content),
			))

		case modelgateway.RoleTool:
			return anthropic.MessageNewParams{},
				errors.New("anthropic tool result requires tool protocol; not implemented in M1")

		default:
			return anthropic.MessageNewParams{},
				fmt.Errorf("unsupported anthropic message role: %q", message.Role)
		}
	}

	if len(messages) == 0 {
		return anthropic.MessageNewParams{}, errors.New("anthropic requires at least one user or assistant message")
	}

	maxTokens := int64(req.MaxOutputTokens)
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(modelName),
		MaxTokens: maxTokens,
		Messages:  messages,
		System:    system,
	}

	// if req.Temperature != nil {
	// 	params.Temperature = anthropic.Float(*req.Temperature)
	// }
	// if req.TopP != nil {
	// 	params.TopP = anthropic.Float(*req.TopP)
	// }
	if len(req.StopSequences) > 0 {
		params.StopSequences = req.StopSequences
	}

	return params, nil
}

func mapResponse(
	message *anthropic.Message,
	req modelgateway.GenerateRequest,
) *modelgateway.GenerateResponse {
	var content strings.Builder

	for _, block := range message.Content {
		if textBlock, ok := block.AsAny().(anthropic.TextBlock); ok {
			content.WriteString(textBlock.Text)
		}
	}

	result := &modelgateway.GenerateResponse{
		ID:         message.ID,
		ProviderID: providerID,
		ModelID:    req.ModelID,
		Model:      string(message.Model),
		Content:    content.String(),
		Usage: modelgateway.Usage{
			InputTokens:  int64(message.Usage.InputTokens),
			OutputTokens: int64(message.Usage.OutputTokens),
		},
	}

	switch string(message.StopReason) {
	case "end_turn", "stop_sequence":
		result.FinishReason = modelgateway.FinishReasonStop
	case "max_tokens":
		result.FinishReason = modelgateway.FinishReasonLength
	default:
		result.FinishReason = modelgateway.FinishReasonUnknown
	}

	result.CreatedAt = time.Now().UTC()
	result.Normalize()
	return result
}

func send(
	ctx context.Context,
	out chan<- modelgateway.StreamEvent,
	event modelgateway.StreamEvent,
) bool {
	select {
	case out <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return provider.NewError(providerID, provider.ErrorCodeCanceled, "request canceled", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return provider.NewError(providerID, provider.ErrorCodeTimeout, "request deadline exceeded", err)
	}

	var statusErr interface{ StatusCode() int }
	if errors.As(err, &statusErr) {
		status := statusErr.StatusCode()
		code := provider.ErrorCodeProvider
		retryable := false

		switch status {
		case 400, 422:
			code = provider.ErrorCodeInvalidRequest
		case 401:
			code = provider.ErrorCodeAuthentication
		case 403:
			code = provider.ErrorCodePermission
		case 404:
			code = provider.ErrorCodeModelNotFound
		case 408:
			code, retryable = provider.ErrorCodeTimeout, true
		case 429:
			code, retryable = provider.ErrorCodeRateLimit, true
		default:
			if status >= 500 {
				code, retryable = provider.ErrorCodeUnavailable, true
			}
		}

		return &provider.Error{
			ProviderID: providerID,
			Code:       code,
			Message:    "Anthropic API request failed",
			StatusCode: status,
			Retryable:  retryable,
			Cause:      err,
		}
	}

	return provider.NewError(providerID, provider.ErrorCodeProvider, "Anthropic request failed", err)
}

type nativeMessageResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Content []struct {
		Type  string         `json:"type"`
		Text  string         `json:"text"`
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Input map[string]any `json:"input"`
	} `json:"content"`
	Usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	StopReason string `json:"stop_reason"`
}

func (a *Adapter) generateNative(ctx context.Context, req modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	if strings.TrimSpace(req.ProviderModel) == "" {
		return nil, errors.New("Anthropic model is required")
	}
	system := make([]string, 0)
	messages := make([]map[string]any, 0, len(req.Messages))
	if strings.TrimSpace(req.System) != "" {
		system = append(system, req.System)
	}
	for _, msg := range req.Messages {
		switch msg.Role {
		case modelgateway.RoleSystem, modelgateway.RoleDeveloper:
			system = append(system, msg.Content)
		case modelgateway.RoleUser:
			messages = append(messages, map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": msg.Content}}})
		case modelgateway.RoleAssistant:
			blocks := make([]any, 0)
			if msg.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": msg.Content})
			}
			for _, call := range msg.ToolCalls {
				var input map[string]any
				if err := json.Unmarshal(call.Arguments, &input); err != nil {
					return nil, modelgateway.ErrInvalidToolCall
				}
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": input})
			}
			if len(blocks) > 0 {
				messages = append(messages, map[string]any{"role": "assistant", "content": blocks})
			}
		case modelgateway.RoleTool:
			messages = append(messages, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": msg.ToolCallID, "content": msg.Content}}})
		default:
			return nil, fmt.Errorf("unsupported Anthropic role: %q", msg.Role)
		}
	}
	toolDefs := make([]map[string]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		toolDefs = append(toolDefs, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": tool.Parameters})
	}
	maxTokens := req.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	payload := map[string]any{"model": req.ProviderModel, "max_tokens": maxTokens, "messages": messages, "tools": toolDefs}
	if len(system) > 0 {
		payload["system"] = strings.Join(system, "\n\n")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(a.baseURL, "/") + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")
	client := a.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpReq)
	if err != nil {
		return nil, normalizeError(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, fmt.Errorf("Anthropic Messages API failed (HTTP %d)", response.StatusCode)
	}
	var decoded nativeMessageResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode Anthropic response: %w", err)
	}
	result := &modelgateway.GenerateResponse{ID: decoded.ID, ProviderID: providerID, ModelID: req.ModelID, Model: decoded.Model, Usage: modelgateway.Usage{InputTokens: decoded.Usage.InputTokens, OutputTokens: decoded.Usage.OutputTokens}}
	for _, block := range decoded.Content {
		if block.Type == "text" {
			result.Content += block.Text
		}
		if block.Type == "tool_use" {
			args, err := json.Marshal(block.Input)
			if err != nil {
				return nil, err
			}
			result.ToolCalls = append(result.ToolCalls, modelgateway.ToolCall{ID: block.ID, Name: block.Name, Arguments: args})
		}
	}
	if len(result.ToolCalls) > 0 {
		result.FinishReason = modelgateway.FinishReasonToolCall
	} else {
		result.FinishReason = modelgateway.FinishReasonStop
	}
	result.Normalize()
	return result, nil
}
