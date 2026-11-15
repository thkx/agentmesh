package airesource

import (
	"context"
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

func (r *ModelGatewayRouter) Generate(ctx context.Context, req modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	if r == nil || r.router == nil {
		return nil, errors.New("dynamic model gateway router is not initialized")
	}
	if len(req.Tools) > 0 {
		return nil, modelgateway.ErrNativeToolCallingUnsupported
	}
	messages := make([]Message, 0, len(req.Messages))
	for _, message := range req.Messages {
		messages = append(messages, Message{Role: string(message.Role), Content: message.Content})
	}
	result, err := r.router.Generate(ctx, GenerateRequest{
		Model: req.ProviderModel, Messages: messages,
		Temperature: req.Temperature, TopP: req.TopP,
		MaxTokens: req.MaxOutputTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("dynamic credential route: %w", err)
	}
	out := &modelgateway.GenerateResponse{
		ID: result.ID, Model: result.Model,
		CreatedAt: time.Unix(result.Created, 0).UTC(),
	}
	if len(result.Choices) > 0 {
		out.Content = result.Choices[0].Message.Content
		out.FinishReason = modelgateway.FinishReason(result.Choices[0].FinishReason)
	}
	out.Usage = modelgateway.Usage{
		InputTokens:  int64(result.Usage.PromptTokens),
		OutputTokens: int64(result.Usage.CompletionTokens),
		TotalTokens:  int64(result.Usage.TotalTokens),
	}
	return out, nil
}
