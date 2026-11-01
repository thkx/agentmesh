package modelgateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
)

var (
	ErrNilRegistry                    = errors.New("model registry is nil")
	ErrNilResolver                    = errors.New("model resolver is nil")
	ErrNilResponse                    = errors.New("provider returned nil response")
	ErrNilStream                      = errors.New("provider returned nil stream")
	ErrNilProviderRegistry            = errors.New("provider registry is nil")
	ErrStreamingDisabled              = errors.New("model streaming is disabled")
	ErrOutputTokenLimitExceeded       = errors.New("requested output tokens exceed model limit")
	ErrInvalidStreamEvent             = errors.New("provider returned an invalid stream event")
	ErrNativeToolCallingUnsupported   = errors.New("provider adapter does not support native tool calling")
	ErrNativeToolStreamingUnsupported = errors.New("provider adapter does not support native tool calling in streaming mode")
)

type Gateway struct {
	registry  *Registry
	resolver  Resolver
	providers *provider.ProviderRegistry
}

func NewGateway(registry *Registry, resolver Resolver) (*Gateway, error) {
	if registry == nil {
		return nil, ErrNilRegistry
	}

	if resolver == nil {
		return nil, ErrNilResolver
	}

	return &Gateway{
		registry: registry,
		resolver: resolver,
	}, nil
}

// NewGatewayWithProviders additionally enforces provider-instance enabled state.
func NewGatewayWithProviders(registry *Registry, resolver Resolver, providers *provider.ProviderRegistry) (*Gateway, error) {
	if providers == nil {
		return nil, ErrNilProviderRegistry
	}
	g, err := NewGateway(registry, resolver)
	if err != nil {
		return nil, err
	}
	g.providers = providers
	return g, nil
}

// Generate executes a non-streaming model request.
func (g *Gateway) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate request: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, NormalizeError("", err)
	}

	model, adapter, resolvedReq, err := g.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(req.Tools) > 0 {
		capability, ok := adapter.(interface{ SupportsNativeToolCalling() bool })
		if !ok || !capability.SupportsNativeToolCalling() {
			return nil, ErrNativeToolCallingUnsupported
		}
	}

	resp, err := adapter.Generate(ctx, resolvedReq)
	if err != nil {
		return nil, NormalizeError(model.ProviderID, err)
	}

	if resp == nil {
		return nil, ErrNilResponse
	}

	normalizeResponse(resp, model)

	return resp, nil
}

// Stream executes a streaming model request.
//
// Errors occurring before the stream starts are returned directly.
// Errors occurring after the stream starts are delivered as
// StreamEventError events.
//
// The returned channel is closed when the provider stream ends
// or the request context is canceled.
func (g *Gateway) Stream(ctx context.Context, req GenerateRequest) (<-chan StreamEvent, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate request: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, NormalizeError("", err)
	}

	model, adapter, resolvedReq, err := g.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(req.Tools) > 0 {
		capability, ok := adapter.(interface{ SupportsNativeToolStreaming() bool })
		if !ok || !capability.SupportsNativeToolStreaming() {
			return nil, ErrNativeToolStreamingUnsupported
		}
	}

	if !model.SupportsStreaming {
		return nil, fmt.Errorf(
			"%w: %s",
			ErrStreamingDisabled,
			model.ID,
		)
	}

	source, err := adapter.Stream(ctx, resolvedReq)
	if err != nil {
		return nil, NormalizeError(model.ProviderID, err)
	}

	if source == nil {
		return nil, ErrNilStream
	}

	output := make(chan StreamEvent)

	go func() {
		defer close(output)

		for {
			select {
			case <-ctx.Done():
				return

			case event, ok := <-source:
				if !ok {
					return
				}

				normalizeStreamEvent(&event, model)

				select {
				case output <- event:
				case <-ctx.Done():
					return
				}

				if event.Type == StreamEventError || event.Type == StreamEventCompleted {
					return
				}
			}
		}
	}()

	return output, nil
}

// SupportsNativeToolCalling reports whether the resolved adapter implements provider-native function calling.
func (g *Gateway) SupportsNativeToolCalling(ctx context.Context, modelID string) bool {
	_, adapter, _, err := g.resolve(ctx, GenerateRequest{ModelID: modelID, Messages: []Message{{Role: RoleUser, Content: "capability probe"}}})
	if err != nil {
		return false
	}
	capability, ok := adapter.(interface{ SupportsNativeToolCalling() bool })
	return ok && capability.SupportsNativeToolCalling()
}

func (g *Gateway) resolve(ctx context.Context, req GenerateRequest) (provider.Model, Model, GenerateRequest, error) {
	model, err := g.resolver.Resolve(ctx, req.ModelID)
	if err != nil {
		return provider.Model{}, nil, GenerateRequest{},
			fmt.Errorf("resolve model: %w", err)
	}

	if err := model.Validate(); err != nil {
		return provider.Model{}, nil, GenerateRequest{},
			fmt.Errorf("invalid resolved model: %w", err)
	}

	if !model.Enabled {
		return provider.Model{}, nil, GenerateRequest{},
			fmt.Errorf("%w: %s", provider.ErrModelDisabled, model.ID)
	}

	// The model catalog is the authority for the maximum output size.
	// Input token counting is intentionally left to provider tokenizers; the
	// gateway must not pretend that character counts are token counts.
	if model.MaxOutputTokens > 0 && req.MaxOutputTokens > model.MaxOutputTokens {
		return provider.Model{}, nil, GenerateRequest{}, fmt.Errorf(
			"%w: requested=%d allowed=%d model=%s",
			ErrOutputTokenLimitExceeded,
			req.MaxOutputTokens,
			model.MaxOutputTokens,
			model.ID,
		)
	}

	var providerInstance *provider.Provider
	if g.providers != nil {
		p, providerErr := g.providers.Get(model.ProviderID)
		if providerErr != nil {
			return provider.Model{}, nil, GenerateRequest{}, fmt.Errorf("resolve provider instance: %w", providerErr)
		}
		providerInstance = &p
		if !p.Enabled {
			return provider.Model{}, nil, GenerateRequest{}, fmt.Errorf("%w: %s", provider.ErrProviderDisabled, p.ID)
		}
	}

	adapter, err := g.registry.Get(model.ProviderID)
	if err != nil {
		if providerInstance != nil && providerInstance.Kind == provider.KindCloud {
			return provider.Model{}, nil, GenerateRequest{}, fmt.Errorf("%w: provider %s", appcredential.ErrCredentialUnavailable, model.ProviderID)
		}
		return provider.Model{}, nil, GenerateRequest{}, fmt.Errorf("resolve provider adapter: %w", err)
	}

	resolvedReq := req
	resolvedReq.ProviderID = model.ProviderID
	resolvedReq.ProviderModel = model.Name

	return model, adapter, resolvedReq, nil
}

func normalizeResponse(resp *GenerateResponse, model provider.Model) {
	// The resolved provider instance is authoritative; adapters may only know vendor type.
	resp.ProviderID = model.ProviderID

	resp.ModelID = model.ID

	resp.Model = model.Name

	resp.Normalize()
}

func normalizeStreamEvent(event *StreamEvent, model provider.Model) {
	event.ProviderID = model.ProviderID

	event.ModelID = model.ID

	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}

	switch event.Type {
	case StreamEventTextDelta:
	case StreamEventUsage:
		if event.Usage != nil {
			event.Usage.Normalize()
		}

	case StreamEventCompleted:
		if event.Response != nil {
			normalizeResponse(event.Response, model)
		}

	case StreamEventError:
		if event.Error == nil {
			event.Error = errors.New("provider stream failed")
		}

		event.Error = NormalizeError(model.ProviderID, event.Error)

	default:
		invalidType := event.Type
		event.Type = StreamEventError
		event.Error = fmt.Errorf("%w: %q", ErrInvalidStreamEvent, invalidType)
		event.Error = NormalizeError(model.ProviderID, event.Error)
	}
}

// NormalizeError converts errors into AgentMesh provider errors.
//
// Existing provider errors are preserved.
// Context cancellation and timeout are classified explicitly.
func NormalizeError(providerID provider.ID, err error) error {
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
		return &provider.Error{
			ProviderID: providerID,
			Code:       provider.ErrorCodeTimeout,
			Message:    "request deadline exceeded",
			Retryable:  true,
			Cause:      err,
		}
	}

	var existing *provider.Error
	if errors.As(err, &existing) {
		if providerID == "" || existing.ProviderID == providerID {
			return existing
		}
		copy := *existing
		copy.ProviderID = providerID
		return &copy
	}

	var statusError interface {
		StatusCode() int
	}

	if errors.As(err, &statusError) {
		status := statusError.StatusCode()

		switch status {
		case 400, 422:
			return &provider.Error{
				ProviderID: providerID,
				Code:       provider.ErrorCodeInvalidRequest,
				Message:    "provider rejected request",
				StatusCode: status,
				Cause:      err,
			}

		case 401:
			return &provider.Error{
				ProviderID: providerID,
				Code:       provider.ErrorCodeAuthentication,
				Message:    "provider authentication failed",
				StatusCode: status,
				Cause:      err,
			}

		case 403:
			return &provider.Error{
				ProviderID: providerID,
				Code:       provider.ErrorCodePermission,
				Message:    "provider permission denied",
				StatusCode: status,
				Cause:      err,
			}

		case 404:
			return &provider.Error{
				ProviderID: providerID,
				Code:       provider.ErrorCodeModelNotFound,
				Message:    "provider model or endpoint not found",
				StatusCode: status,
				Cause:      err,
			}

		case 408:
			return &provider.Error{
				ProviderID: providerID,
				Code:       provider.ErrorCodeTimeout,
				Message:    "provider request timed out",
				StatusCode: status,
				Retryable:  true,
				Cause:      err,
			}

		case 429:
			return &provider.Error{
				ProviderID: providerID,
				Code:       provider.ErrorCodeRateLimit,
				Message:    "provider rate limit exceeded",
				StatusCode: status,
				Retryable:  true,
				Cause:      err,
			}
		}

		if status >= 500 {
			return &provider.Error{
				ProviderID: providerID,
				Code:       provider.ErrorCodeUnavailable,
				Message:    "provider service unavailable",
				StatusCode: status,
				Retryable:  true,
				Cause:      err,
			}
		}
	}

	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return &provider.Error{
			ProviderID: providerID,
			Code:       provider.ErrorCodeTimeout,
			Message:    "network request timed out",
			Retryable:  true,
			Cause:      err,
		}
	}

	return provider.NewError(
		providerID,
		provider.ErrorCodeProvider,
		err.Error(),
		err,
	)
}
