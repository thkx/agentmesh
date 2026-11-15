package airesource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

// ModelGatewayRouter adapts the existing AI resource pool to the shared
// AgentMesh model gateway. Credential selection, retries, health and usage
// accounting remain owned by airesource.Router.
type ModelGatewayRouter struct {
	service *Service
	router  *Router
}

func NewModelGatewayRouter(service *Service, router *Router) *ModelGatewayRouter {
	return &ModelGatewayRouter{service: service, router: router}
}

// ResolveModel exposes AI Resource models to AgentMesh by ai_models.id.
// The provider ID is a logical namespace, not a legacy ProviderRegistry entry.
func (r *ModelGatewayRouter) ResolveModel(ctx context.Context, modelID string) (provider.Model, bool, error) {
	if r == nil || r.service == nil || r.router == nil {
		return provider.Model{}, false, errors.New("dynamic model gateway router is not initialized")
	}
	m, err := r.service.GetModelByID(ctx, modelID)
	if errors.Is(err, ErrNotFound) {
		return provider.Model{}, false, nil
	}
	if err != nil {
		return provider.Model{}, false, err
	}
	return provider.Model{
		ID: m.ID, ProviderID: provider.ID("ai-resource"), Name: m.ModelKey,
		DisplayName: m.DisplayName, SupportsStreaming: m.SupportsStreaming,
		Enabled: m.Enabled, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}, true, nil
}

func (r *ModelGatewayRouter) Supports(ctx context.Context, modelKey string) (bool, error) {
	if r == nil || r.service == nil || r.router == nil {
		return false, errors.New("dynamic model gateway router is not initialized")
	}
	return r.service.HasModelKey(ctx, modelKey)
}

// SupportsNativeToolCalling reports whether every currently eligible credential
// for this model uses the OpenAI-compatible protocol supported by the tool bridge.
func (r *ModelGatewayRouter) SupportsNativeToolCalling(ctx context.Context, modelKey string) (bool, error) {
	if r == nil || r.service == nil || r.router == nil {
		return false, errors.New("dynamic model gateway router is not initialized")
	}
	candidates, _, err := r.service.Candidates(ctx, modelKey)
	if err != nil {
		return false, err
	}
	if len(candidates) == 0 {
		return false, nil
	}
	for _, candidate := range candidates {
		if candidate.Provider.Protocol != ProtocolOpenAI {
			return false, nil
		}
	}
	return true, nil
}

func (r *ModelGatewayRouter) Generate(ctx context.Context, req modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	if r == nil || r.router == nil {
		return nil, errors.New("dynamic model gateway router is not initialized")
	}
	messages := make([]Message, 0, len(req.Messages))
	for _, message := range req.Messages {
		item := Message{Role: string(message.Role), Content: message.Content, Name: message.Name, ToolCallID: message.ToolCallID}
		for _, call := range message.ToolCalls {
			item.ToolCalls = append(item.ToolCalls, ToolCall{
				ID: call.ID, Type: "function",
				Function: ToolCallFunction{Name: call.Name, Arguments: string(call.Arguments)},
			})
		}
		messages = append(messages, item)
	}
	tools := make([]Tool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, Tool{Type: "function", Function: ToolFunction{
			Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters,
		}})
	}
	result, err := r.router.Generate(ctx, GenerateRequest{
		Model: req.ProviderModel, Messages: messages,
		Temperature: req.Temperature, TopP: req.TopP,
		MaxTokens: req.MaxOutputTokens, Tools: tools, ToolChoice: req.ToolChoice,
	})
	if err != nil {
		if errors.Is(err, ErrNativeToolCallingUnsupported) {
			return nil, modelgateway.ErrNativeToolCallingUnsupported
		}
		return nil, fmt.Errorf("dynamic credential route: %w", err)
	}
	out := &modelgateway.GenerateResponse{
		ID: result.ID, Model: result.Model,
		CreatedAt: time.Unix(result.Created, 0).UTC(),
	}
	if len(result.Choices) > 0 {
		choice := result.Choices[0]
		out.Content = choice.Message.Content
		out.FinishReason = modelgateway.FinishReason(choice.FinishReason)
		for _, call := range choice.Message.ToolCalls {
			arguments := json.RawMessage(call.Function.Arguments)
			if !json.Valid(arguments) {
				return nil, fmt.Errorf("dynamic provider returned invalid tool arguments")
			}
			out.ToolCalls = append(out.ToolCalls, modelgateway.ToolCall{
				ID: call.ID, Name: call.Function.Name, Arguments: arguments,
			})
		}
	}
	out.Usage = modelgateway.Usage{
		InputTokens:  int64(result.Usage.PromptTokens),
		OutputTokens: int64(result.Usage.CompletionTokens),
		TotalTokens:  int64(result.Usage.TotalTokens),
	}
	return out, nil
}
