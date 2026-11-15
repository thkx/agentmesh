package openai

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

	sdk "github.com/openai/openai-go/v3"
	sdkoption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

const providerID provider.ID = provider.IDOpenAI

// Adapter implements modelgateway.Model using the OpenAI Responses API.
type Adapter struct {
	client     sdk.Client
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// New creates an OpenAI adapter.
//
// The API key should eventually be supplied by the encrypted credential
// repository. It must never be logged.
func New(apiKey string, options ...sdkoption.RequestOption) (*Adapter, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("openai api key is required")
	}

	clientOptions := make([]sdkoption.RequestOption, 0, len(options)+1)
	clientOptions = append(clientOptions, sdkoption.WithAPIKey(apiKey))
	clientOptions = append(clientOptions, options...)

	return &Adapter{client: sdk.NewClient(clientOptions...), apiKey: apiKey, baseURL: "https://api.openai.com/v1", httpClient: &http.Client{Timeout: 0, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// NewWithClient supports dependency injection and testing.
func NewWithClient(client sdk.Client) *Adapter {
	return &Adapter{client: client}
}

// NewWithBaseURL creates an adapter with an explicit Responses API endpoint.
func NewWithBaseURL(apiKey, baseURL string) (*Adapter, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	adapter, err := New(apiKey, sdkoption.WithBaseURL(baseURL))
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

func (a *Adapter) Generate(ctx context.Context, req modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	if len(req.Tools) > 0 && a.SupportsNativeToolCalling() {
		return a.generateNative(ctx, req)
	}
	if err := ctx.Err(); err != nil {
		return nil, normalizeOpenAIError(err)
	}

	params, err := buildRequest(req)
	if err != nil {
		return nil, err
	}

	resp, err := a.client.Responses.New(ctx, params)
	if err != nil {
		return nil, normalizeOpenAIError(err)
	}
	if resp == nil {
		return nil, errors.New("openai returned nil response")
	}

	return mapResponse(resp, req), nil
}

func (a *Adapter) Stream(ctx context.Context, req modelgateway.GenerateRequest) (<-chan modelgateway.StreamEvent, error) {
	if len(req.Tools) > 0 {
		return nil, errors.New("OpenAI native tool streaming is not implemented")
	}
	if err := ctx.Err(); err != nil {
		return nil, normalizeOpenAIError(err)
	}

	params, err := buildRequest(req)
	if err != nil {
		return nil, err
	}

	stream := a.client.Responses.NewStreaming(ctx, params)
	if stream == nil {
		return nil, errors.New("openai returned nil stream")
	}

	output := make(chan modelgateway.StreamEvent)

	go func() {
		defer close(output)
		defer func() {
			_ = stream.Close()
		}()

		for stream.Next() {
			if err := ctx.Err(); err != nil {
				return
			}

			event := stream.Current()

			switch event.Type {
			case "response.output_text.delta":
				if event.Delta == "" {
					continue
				}

				if !sendEvent(ctx, output, modelgateway.NewTextDeltaEvent(
					providerID,
					req.ModelID,
					event.Delta,
				)) {
					return
				}

			case "response.completed":
				completed := event.Response

				usage := mapUsage(completed.Usage)

				if !sendEvent(ctx, output, modelgateway.NewUsageEvent(
					providerID,
					req.ModelID,
					usage,
				)) {
					return
				}

				response := mapResponse(&completed, req)

				if !sendEvent(ctx, output, modelgateway.NewCompletedEvent(response)) {
					return
				}

				return

			case "response.failed":
				err := errors.New("openai response failed")
				if event.Response.Error.Message != "" {
					err = errors.New(event.Response.Error.Message)
				}

				sendEvent(ctx, output, modelgateway.NewErrorEvent(
					normalizeOpenAIError(err),
				))
				return

			case "response.incomplete":
				err := errors.New("openai response incomplete")
				if event.Response.IncompleteDetails.Reason != "" {
					err = fmt.Errorf(
						"openai response incomplete: %s",
						event.Response.IncompleteDetails.Reason,
					)
				}

				sendEvent(ctx, output, modelgateway.NewErrorEvent(
					provider.NewError(
						providerID,
						provider.ErrorCodeProvider,
						err.Error(),
						err,
					),
				))
				return

			case "error":
				message := event.Message
				if message == "" {
					message = "openai streaming error"
				}

				sendEvent(ctx, output, modelgateway.NewErrorEvent(
					normalizeOpenAIError(errors.New(message)),
				))
				return
			}
		}

		if err := stream.Err(); err != nil {
			sendEvent(ctx, output, modelgateway.NewErrorEvent(
				normalizeOpenAIError(err),
			))
			return
		}

		// A stream ending without response.completed is not success.
		sendEvent(ctx, output, modelgateway.NewErrorEvent(
			errors.New("openai stream ended without completion event"),
		))
	}()

	return output, nil
}

func buildRequest(req modelgateway.GenerateRequest) (responses.ResponseNewParams, error) {
	modelName := strings.TrimSpace(req.ProviderModel)
	if modelName == "" {
		return responses.ResponseNewParams{},
			errors.New("openai provider model is required")
	}

	instructions := make([]string, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.System) != "" {
		instructions = append(instructions, req.System)
	}

	input := make(responses.ResponseInputParam, 0, len(req.Messages))

	for _, message := range req.Messages {
		switch message.Role {
		case modelgateway.RoleSystem, modelgateway.RoleDeveloper:
			instructions = append(instructions, message.Content)

		case modelgateway.RoleUser:
			input = append(input,
				responses.ResponseInputItemParamOfMessage(
					message.Content,
					responses.EasyInputMessageRoleUser,
				),
			)

		case modelgateway.RoleAssistant:
			input = append(input,
				responses.ResponseInputItemParamOfMessage(
					message.Content,
					responses.EasyInputMessageRoleAssistant,
				),
			)

		case modelgateway.RoleTool:
			return responses.ResponseNewParams{},
				errors.New(
					"tool messages require a tool call identifier; " +
						"tool execution protocol is not implemented in M1",
				)

		default:
			return responses.ResponseNewParams{},
				fmt.Errorf("unsupported message role: %q", message.Role)
		}
	}

	if len(input) == 0 {
		return responses.ResponseNewParams{},
			errors.New("openai input must contain user or assistant messages")
	}

	params := responses.ResponseNewParams{
		Model: sdk.ChatModel(modelName),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: input,
		},
	}

	if len(instructions) > 0 {
		params.Instructions = sdk.String(strings.Join(instructions, "\n\n"))
	}

	if req.MaxOutputTokens > 0 {
		params.MaxOutputTokens = sdk.Int(int64(req.MaxOutputTokens))
	}

	if req.Temperature != nil {
		params.Temperature = sdk.Float(*req.Temperature)
	}

	if req.TopP != nil {
		params.TopP = sdk.Float(*req.TopP)
	}

	return params, nil
}
func mapResponse(resp *responses.Response, req modelgateway.GenerateRequest) *modelgateway.GenerateResponse {
	result := &modelgateway.GenerateResponse{
		ID:         resp.ID,
		ProviderID: providerID,
		ModelID:    req.ModelID,
		Model:      string(resp.Model),
		Content:    resp.OutputText(),
		Usage:      mapUsage(resp.Usage),
	}

	switch string(resp.Status) {
	case "completed":
		result.FinishReason = modelgateway.FinishReasonStop
	case "incomplete":
		result.FinishReason = modelgateway.FinishReasonLength
	default:
		result.FinishReason = modelgateway.FinishReasonUnknown
	}

	if resp.CreatedAt > 0 {
		result.CreatedAt = time.Unix(
			int64(resp.CreatedAt),
			int64((resp.CreatedAt-float64(int64(resp.CreatedAt)))*float64(time.Second)),
		).UTC()
	}

	result.Normalize()

	return result
}

func mapUsage(usage responses.ResponseUsage) modelgateway.Usage {
	result := modelgateway.Usage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
	}

	if usage.JSON.InputTokensDetails.Valid() {
		result.CachedInputTokens = usage.InputTokensDetails.CachedTokens
	}

	if usage.JSON.OutputTokensDetails.Valid() {
		result.ReasoningTokens = usage.OutputTokensDetails.ReasoningTokens
	}

	result.Normalize()
	return result
}

func sendEvent(ctx context.Context, output chan<- modelgateway.StreamEvent, event modelgateway.StreamEvent) bool {
	select {
	case output <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func normalizeOpenAIError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.Canceled) {
		return provider.NewError(
			providerID,
			provider.ErrorCodeCanceled,
			"request canceled",
			err,
		)
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return provider.NewError(
			providerID,
			provider.ErrorCodeTimeout,
			"request deadline exceeded",
			err,
		)
	}

	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		status := apiErr.StatusCode

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
			code = provider.ErrorCodeTimeout
			retryable = true
		case 429:
			code = provider.ErrorCodeRateLimit
			retryable = true
		default:
			if status >= 500 {
				code = provider.ErrorCodeUnavailable
				retryable = true
			}
		}

		return &provider.Error{
			ProviderID: providerID,
			Code:       code,
			Message:    fmt.Sprintf("OpenAI API request failed (HTTP %d)", status),
			StatusCode: status,
			Retryable:  retryable,
			Cause:      err,
		}
	}

	return provider.NewError(
		providerID,
		provider.ErrorCodeProvider,
		"OpenAI request failed",
		err,
	)
}

type nativeResponse struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"output"`
	Usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	Status string `json:"status"`
}

func (a *Adapter) generateNative(ctx context.Context, req modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	modelName := strings.TrimSpace(req.ProviderModel)
	if modelName == "" {
		return nil, errors.New("openai provider model is required")
	}
	instructions := make([]string, 0)
	input := make([]map[string]any, 0, len(req.Messages)*2)
	if strings.TrimSpace(req.System) != "" {
		instructions = append(instructions, req.System)
	}
	for _, msg := range req.Messages {
		switch msg.Role {
		case modelgateway.RoleSystem, modelgateway.RoleDeveloper:
			instructions = append(instructions, msg.Content)
		case modelgateway.RoleUser:
			input = append(input, map[string]any{"role": "user", "content": msg.Content})
		case modelgateway.RoleAssistant:
			if msg.Content != "" {
				input = append(input, map[string]any{"role": "assistant", "content": msg.Content})
			}
			for _, call := range msg.ToolCalls {
				input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
			}
		case modelgateway.RoleTool:
			input = append(input, map[string]any{"type": "function_call_output", "call_id": msg.ToolCallID, "output": msg.Content})
		default:
			return nil, fmt.Errorf("unsupported OpenAI message role: %q", msg.Role)
		}
	}
	tools := make([]map[string]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": tool.Parameters, "strict": false})
	}
	payload := map[string]any{"model": modelName, "input": input, "tools": tools}
	if len(instructions) > 0 {
		payload["instructions"] = strings.Join(instructions, "\n\n")
	}
	if req.MaxOutputTokens > 0 {
		payload["max_output_tokens"] = req.MaxOutputTokens
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		payload["top_p"] = *req.TopP
	}
	if strings.TrimSpace(req.ToolChoice) != "" {
		payload["tool_choice"] = req.ToolChoice
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(a.baseURL, "/") + "/responses"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	client := a.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpReq)
	if err != nil {
		return nil, normalizeOpenAIError(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, fmt.Errorf("OpenAI Responses API failed (HTTP %d)", response.StatusCode)
	}
	var decoded nativeResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode OpenAI Responses API response: %w", err)
	}
	result := &modelgateway.GenerateResponse{ID: decoded.ID, ProviderID: providerID, ModelID: req.ModelID, Model: decoded.Model, Usage: modelgateway.Usage{InputTokens: decoded.Usage.InputTokens, OutputTokens: decoded.Usage.OutputTokens}}
	for _, item := range decoded.Output {
		if item.Type == "function_call" {
			args := json.RawMessage(item.Arguments)
			result.ToolCalls = append(result.ToolCalls, modelgateway.ToolCall{ID: item.CallID, Name: item.Name, Arguments: args})
		}
		if item.Type == "message" {
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					result.Content += part.Text
				}
			}
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
