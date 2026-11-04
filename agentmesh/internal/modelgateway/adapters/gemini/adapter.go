package geminiadapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

const providerID provider.ID = provider.IDGemini

type Adapter struct {
	client     *genai.Client
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func New(ctx context.Context, apiKey string) (*Adapter, error) {
	return NewWithBaseURL(ctx, apiKey, "https://generativelanguage.googleapis.com/")
}

// NewWithBaseURL creates an isolated Gemini client with an explicit endpoint.
func NewWithBaseURL(ctx context.Context, apiKey, baseURL string) (*Adapter, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("gemini api key is required")
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://generativelanguage.googleapis.com/"
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:      apiKey,
		Backend:     genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: baseURL},
	})
	if err != nil {
		return nil, fmt.Errorf("create Gemini client: %w", err)
	}

	return &Adapter{client: client, apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), httpClient: &http.Client{Timeout: 0, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func NewWithClient(client *genai.Client) (*Adapter, error) {
	if client == nil {
		return nil, errors.New("gemini client is nil")
	}
	return &Adapter{client: client}, nil
}

var _ modelgateway.Model = (*Adapter)(nil)

func (a *Adapter) Generate(
	ctx context.Context,
	req modelgateway.GenerateRequest,
) (*modelgateway.GenerateResponse, error) {
	if len(req.Tools) > 0 && a.SupportsNativeToolCalling() {
		return a.generateNative(ctx, req)
	}
	modelName, contents, config, err := buildRequest(req)
	if err != nil {
		return nil, err
	}

	resp, err := a.client.Models.GenerateContent(ctx, modelName, contents, config)
	if err != nil {
		return nil, normalizeError(err)
	}
	if resp == nil {
		return nil, errors.New("Gemini returned nil response")
	}

	return mapResponse(resp, req), nil
}

func (a *Adapter) Stream(
	ctx context.Context,
	req modelgateway.GenerateRequest,
) (<-chan modelgateway.StreamEvent, error) {
	if len(req.Tools) > 0 {
		return nil, errors.New("Gemini native tool streaming is not implemented")
	}
	modelName, contents, config, err := buildRequest(req)
	if err != nil {
		return nil, err
	}

	out := make(chan modelgateway.StreamEvent)

	go func() {
		defer close(out)

		var text strings.Builder
		var last *genai.GenerateContentResponse

		for resp, streamErr := range a.client.Models.GenerateContentStream(
			ctx, modelName, contents, config,
		) {
			if ctx.Err() != nil {
				return
			}

			if streamErr != nil {
				send(ctx, out, modelgateway.NewErrorEvent(normalizeError(streamErr)))
				return
			}
			if resp == nil {
				continue
			}

			last = resp

			delta := resp.Text()
			if delta != "" {
				text.WriteString(delta)
				if !send(ctx, out, modelgateway.NewTextDeltaEvent(
					providerID, req.ModelID, delta,
				)) {
					return
				}
			}
		}

		if ctx.Err() != nil {
			return
		}
		if last == nil {
			send(ctx, out, modelgateway.NewErrorEvent(
				errors.New("Gemini stream returned no response"),
			))
			return
		}

		response := mapResponse(last, req)
		response.Content = text.String()

		if !send(ctx, out, modelgateway.NewUsageEvent(
			providerID, req.ModelID, response.Usage,
		)) {
			return
		}

		send(ctx, out, modelgateway.NewCompletedEvent(response))
	}()

	return out, nil
}

func buildRequest(
	req modelgateway.GenerateRequest,
) (string, []*genai.Content, *genai.GenerateContentConfig, error) {
	modelName := strings.TrimSpace(req.ProviderModel)
	if modelName == "" {
		return "", nil, nil, errors.New("Gemini model is required")
	}

	contents := make([]*genai.Content, 0, len(req.Messages))

	var systemParts []string
	if strings.TrimSpace(req.System) != "" {
		systemParts = append(systemParts, req.System)
	}

	for _, message := range req.Messages {
		switch message.Role {
		case modelgateway.RoleSystem, modelgateway.RoleDeveloper:
			systemParts = append(systemParts, message.Content)

		case modelgateway.RoleUser:
			contents = append(contents, genai.NewContentFromText(
				message.Content, genai.RoleUser,
			))

		case modelgateway.RoleAssistant:
			contents = append(contents, genai.NewContentFromText(
				message.Content, genai.RoleModel,
			))

		case modelgateway.RoleTool:
			return "", nil, nil, errors.New(
				"Gemini tool result requires tool protocol; not implemented in M1",
			)

		default:
			return "", nil, nil, fmt.Errorf(
				"unsupported Gemini message role: %q", message.Role,
			)
		}
	}

	if len(contents) == 0 {
		return "", nil, nil, errors.New("Gemini requires at least one user or assistant message")
	}

	config := &genai.GenerateContentConfig{}

	if len(systemParts) > 0 {
		config.SystemInstruction = genai.NewContentFromText(
			strings.Join(systemParts, "\n\n"),
			genai.RoleUser,
		)
	}

	if req.Temperature != nil {
		value := float32(*req.Temperature)
		config.Temperature = &value
	}
	if req.TopP != nil {
		value := float32(*req.TopP)
		config.TopP = &value
	}
	if req.MaxOutputTokens > 0 {
		value := int32(req.MaxOutputTokens)
		config.MaxOutputTokens = value
	}
	if len(req.StopSequences) > 0 {
		config.StopSequences = req.StopSequences
	}

	return modelName, contents, config, nil
}

func mapResponse(
	resp *genai.GenerateContentResponse,
	req modelgateway.GenerateRequest,
) *modelgateway.GenerateResponse {
	result := &modelgateway.GenerateResponse{
		ID:         resp.ResponseID,
		ProviderID: providerID,
		ModelID:    req.ModelID,
		Model:      resp.ModelVersion,
		Content:    resp.Text(),
		CreatedAt:  time.Now().UTC(),
	}

	if resp.UsageMetadata != nil {
		result.Usage = modelgateway.Usage{
			InputTokens:  int64(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int64(resp.UsageMetadata.CandidatesTokenCount),
			CachedInputTokens: int64(
				resp.UsageMetadata.CachedContentTokenCount,
			),
			ReasoningTokens: int64(
				resp.UsageMetadata.ThoughtsTokenCount,
			),
		}
	}

	if len(resp.Candidates) > 0 && resp.Candidates[0] != nil {
		switch string(resp.Candidates[0].FinishReason) {
		case "STOP":
			result.FinishReason = modelgateway.FinishReasonStop
		case "MAX_TOKENS":
			result.FinishReason = modelgateway.FinishReasonLength
		case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT":
			result.FinishReason = modelgateway.FinishReasonContent
		default:
			result.FinishReason = modelgateway.FinishReasonUnknown
		}
	}

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

	var apiErr *genai.APIError
	if errors.As(err, &apiErr) {
		status := apiErr.Code
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
			Message:    "Gemini API request failed",
			StatusCode: status,
			Retryable:  retryable,
			Cause:      err,
		}
	}

	return provider.NewError(providerID, provider.ErrorCodeProvider, "Gemini request failed", err)
}

type nativeGeminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text         string `json:"text"`
				FunctionCall *struct {
					Name string         `json:"name"`
					Args map[string]any `json:"args"`
				} `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

func (a *Adapter) SupportsNativeToolCalling() bool {
	return strings.TrimSpace(a.apiKey) != "" && strings.TrimSpace(a.baseURL) != ""
}
func (a *Adapter) generateNative(ctx context.Context, req modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	if strings.TrimSpace(req.ProviderModel) == "" {
		return nil, errors.New("Gemini model is required")
	}
	contents := make([]map[string]any, 0, len(req.Messages))
	system := make([]string, 0)
	if strings.TrimSpace(req.System) != "" {
		system = append(system, req.System)
	}
	for _, msg := range req.Messages {
		switch msg.Role {
		case modelgateway.RoleSystem, modelgateway.RoleDeveloper:
			system = append(system, msg.Content)
		case modelgateway.RoleUser:
			contents = append(contents, map[string]any{"role": "user", "parts": []any{map[string]any{"text": msg.Content}}})
		case modelgateway.RoleAssistant:
			parts := make([]any, 0)
			if msg.Content != "" {
				parts = append(parts, map[string]any{"text": msg.Content})
			}
			for _, call := range msg.ToolCalls {
				var args map[string]any
				if err := json.Unmarshal(call.Arguments, &args); err != nil {
					return nil, modelgateway.ErrInvalidToolCall
				}
				parts = append(parts, map[string]any{"functionCall": map[string]any{"name": call.Name, "args": args}})
			}
			if len(parts) > 0 {
				contents = append(contents, map[string]any{"role": "model", "parts": parts})
			}
		case modelgateway.RoleTool:
			var response map[string]any
			if err := json.Unmarshal([]byte(msg.Content), &response); err != nil {
				response = map[string]any{"result": msg.Content}
			}
			contents = append(contents, map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": msg.Name, "response": response}}}})
		default:
			return nil, fmt.Errorf("unsupported Gemini role: %q", msg.Role)
		}
	}
	declarations := make([]map[string]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		declarations = append(declarations, map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters})
	}
	payload := map[string]any{"contents": contents, "tools": []any{map[string]any{"functionDeclarations": declarations}}}
	if len(system) > 0 {
		payload["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": strings.Join(system, "\n\n")}}}
	}
	generation := map[string]any{}
	if req.MaxOutputTokens > 0 {
		generation["maxOutputTokens"] = req.MaxOutputTokens
	}
	if req.Temperature != nil {
		generation["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		generation["topP"] = *req.TopP
	}
	if len(generation) > 0 {
		payload["generationConfig"] = generation
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(a.baseURL, "/") + "/v1beta/models/" + url.PathEscape(req.ProviderModel) + ":generateContent?key=" + url.QueryEscape(a.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
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
		return nil, fmt.Errorf("Gemini generateContent API failed (HTTP %d)", response.StatusCode)
	}
	var decoded nativeGeminiResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode Gemini response: %w", err)
	}
	result := &modelgateway.GenerateResponse{ProviderID: providerID, ModelID: req.ModelID, Model: req.ProviderModel, Usage: modelgateway.Usage{InputTokens: decoded.UsageMetadata.PromptTokenCount, OutputTokens: decoded.UsageMetadata.CandidatesTokenCount}}
	for _, candidate := range decoded.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				result.Content += part.Text
			}
			if part.FunctionCall != nil {
				args, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return nil, err
				}
				randomID := make([]byte, 12)
				id := fmt.Sprintf("gemini-%d", time.Now().UnixNano())
				if _, err := rand.Read(randomID); err == nil {
					id = "gemini-" + hex.EncodeToString(randomID)
				}
				result.ToolCalls = append(result.ToolCalls, modelgateway.ToolCall{ID: id, Name: part.FunctionCall.Name, Arguments: args})
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
