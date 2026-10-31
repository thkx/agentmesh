package anthropicadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go/v2"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

const providerID provider.ID = provider.IDAnthropic

type Adapter struct {
	client anthropic.Client
}

func New(apiKey string, options ...anthropicoption.RequestOption) (*Adapter, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("anthropic api key is required")
	}

	opts := make([]anthropicoption.RequestOption, 0, len(options)+1)
	opts = append(opts, anthropicoption.WithAPIKey(apiKey))
	opts = append(opts, options...)

	return &Adapter{client: anthropic.NewClient(opts...)}, nil
}

func NewWithClient(client anthropic.Client) *Adapter {
	return &Adapter{client: client}
}

var _ modelgateway.Model = (*Adapter)(nil)

func (a *Adapter) Generate(
	ctx context.Context,
	req modelgateway.GenerateRequest,
) (*modelgateway.GenerateResponse, error) {
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
