package ollamaadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

const providerID provider.ID = provider.IDOllama

type Adapter struct {
	baseURL string
	client  *http.Client
}

func New(baseURL string, client *http.Client) (*Adapter, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid Ollama base URL: %q", baseURL)
	}

	if client == nil {
		client = &http.Client{}
	}

	return &Adapter{
		baseURL: baseURL,
		client:  client,
	}, nil
}

var _ modelgateway.Model = (*Adapter)(nil)

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

type chatResponse struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Message   struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done       bool   `json:"done"`
	DoneReason string `json:"done_reason"`

	PromptEvalCount int64 `json:"prompt_eval_count"`
	EvalCount       int64 `json:"eval_count"`
}

func (a *Adapter) Generate(
	ctx context.Context,
	req modelgateway.GenerateRequest,
) (*modelgateway.GenerateResponse, error) {
	payload, err := buildRequest(req, false)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal Ollama request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		a.baseURL+"/api/chat",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, normalizeError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeHTTPError(resp)
	}

	var result chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode Ollama response: %w", err)
	}

	return mapResponse(&result, req), nil
}

func (a *Adapter) Stream(
	ctx context.Context,
	req modelgateway.GenerateRequest,
) (<-chan modelgateway.StreamEvent, error) {
	payload, err := buildRequest(req, true)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal Ollama request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		a.baseURL+"/api/chat",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/x-ndjson")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, normalizeError(err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, decodeHTTPError(resp)
	}

	out := make(chan modelgateway.StreamEvent)

	go func() {
		defer close(out)
		defer resp.Body.Close()

		decoder := json.NewDecoder(resp.Body)
		var content strings.Builder
		var final chatResponse
		receivedDone := false

		for {
			if ctx.Err() != nil {
				return
			}

			var chunk chatResponse
			err := decoder.Decode(&chunk)

			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				send(ctx, out, modelgateway.NewErrorEvent(normalizeError(err)))
				return
			}

			if chunk.Message.Content != "" {
				content.WriteString(chunk.Message.Content)

				if !send(ctx, out, modelgateway.NewTextDeltaEvent(
					providerID,
					req.ModelID,
					chunk.Message.Content,
				)) {
					return
				}
			}

			if chunk.Done {
				final = chunk
				receivedDone = true
				break
			}
		}

		if ctx.Err() != nil {
			return
		}

		if !receivedDone {
			send(ctx, out, modelgateway.NewErrorEvent(
				errors.New("Ollama stream ended without done=true"),
			))
			return
		}

		final.Message.Content = content.String()
		response := mapResponse(&final, req)

		if !send(ctx, out, modelgateway.NewUsageEvent(
			providerID,
			req.ModelID,
			response.Usage,
		)) {
			return
		}

		send(ctx, out, modelgateway.NewCompletedEvent(response))
	}()

	return out, nil
}

func buildRequest(
	req modelgateway.GenerateRequest,
	stream bool,
) (chatRequest, error) {
	modelName := strings.TrimSpace(req.ProviderModel)
	if modelName == "" {
		return chatRequest{}, errors.New("Ollama model is required")
	}

	messages := make([]chatMessage, 0, len(req.Messages)+1)

	if strings.TrimSpace(req.System) != "" {
		messages = append(messages, chatMessage{
			Role:    "system",
			Content: req.System,
		})
	}

	for _, message := range req.Messages {
		role := ""

		switch message.Role {
		case modelgateway.RoleSystem, modelgateway.RoleDeveloper:
			role = "system"
		case modelgateway.RoleUser:
			role = "user"
		case modelgateway.RoleAssistant:
			role = "assistant"
		case modelgateway.RoleTool:
			role = "tool"
		default:
			return chatRequest{}, fmt.Errorf(
				"unsupported Ollama message role: %q",
				message.Role,
			)
		}

		messages = append(messages, chatMessage{
			Role:    role,
			Content: message.Content,
		})
	}

	if len(messages) == 0 {
		return chatRequest{}, errors.New("Ollama requires at least one message")
	}

	options := make(map[string]any)

	if req.Temperature != nil {
		options["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		options["top_p"] = *req.TopP
	}
	if req.MaxOutputTokens > 0 {
		options["num_predict"] = req.MaxOutputTokens
	}
	if len(req.StopSequences) > 0 {
		options["stop"] = req.StopSequences
	}

	if len(options) == 0 {
		options = nil
	}

	return chatRequest{
		Model:    modelName,
		Messages: messages,
		Stream:   stream,
		Options:  options,
	}, nil
}

func mapResponse(
	resp *chatResponse,
	req modelgateway.GenerateRequest,
) *modelgateway.GenerateResponse {
	result := &modelgateway.GenerateResponse{
		ProviderID: providerID,
		ModelID:    req.ModelID,
		Model:      resp.Model,
		Content:    resp.Message.Content,
		Usage: modelgateway.Usage{
			InputTokens:  resp.PromptEvalCount,
			OutputTokens: resp.EvalCount,
		},
		CreatedAt: time.Now().UTC(),
	}

	switch resp.DoneReason {
	case "stop":
		result.FinishReason = modelgateway.FinishReasonStop
	case "length":
		result.FinishReason = modelgateway.FinishReasonLength
	default:
		result.FinishReason = modelgateway.FinishReasonUnknown
	}

	if resp.CreatedAt != "" {
		if createdAt, err := time.Parse(time.RFC3339Nano, resp.CreatedAt); err == nil {
			result.CreatedAt = createdAt.UTC()
		}
	}

	result.Normalize()
	return result
}

func decodeHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}

	code := provider.ErrorCodeProvider
	retryable := false

	switch resp.StatusCode {
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
		if resp.StatusCode >= 500 {
			code, retryable = provider.ErrorCodeUnavailable, true
		}
	}

	return &provider.Error{
		ProviderID: providerID,
		Code:       code,
		Message:    message,
		StatusCode: resp.StatusCode,
		Retryable:  retryable,
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

	return provider.NewError(providerID, provider.ErrorCodeProvider, "Ollama request failed", err)
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
