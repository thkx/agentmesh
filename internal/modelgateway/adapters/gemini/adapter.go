package geminiadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

const providerID provider.ID = provider.IDGemini

type Adapter struct {
	client *genai.Client
}

func New(ctx context.Context, apiKey string) (*Adapter, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("gemini api key is required")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("create Gemini client: %w", err)
	}

	return &Adapter{client: client}, nil
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
