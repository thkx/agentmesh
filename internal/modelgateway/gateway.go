package modelgateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

var (
	ErrNilRegistry       = errors.New("model registry is nil")
	ErrNilResolver       = errors.New("model resolver is nil")
	ErrNilResponse       = errors.New("provider returned nil response")
	ErrNilStream         = errors.New("provider returned nil stream")
	ErrStreamingDisabled = errors.New("model streaming is disabled")
)

type Gateway struct {
	registry *Registry
	resolver Resolver
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

				if event.Type == StreamEventError {
					return
				}

				if event.Type == StreamEventCompleted {
					return
				}
			}
		}
	}()

	return output, nil
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

	adapter, err := g.registry.Get(model.ProviderID)
	if err != nil {
		return provider.Model{}, nil, GenerateRequest{},
			fmt.Errorf("resolve provider adapter: %w", err)
	}

	resolvedReq := req
	resolvedReq.ProviderID = model.ProviderID
	resolvedReq.ProviderModel = model.Name

	return model, adapter, resolvedReq, nil
}

func normalizeResponse(resp *GenerateResponse, model provider.Model) {
	if resp.ProviderID == "" {
		resp.ProviderID = model.ProviderID
	}

	if resp.ModelID == "" {
		resp.ModelID = model.ID
	}

	if resp.Model == "" {
		resp.Model = model.Name
	}

	resp.Normalize()
}

func normalizeStreamEvent(event *StreamEvent, model provider.Model) {
	if event.ProviderID == "" {
		event.ProviderID = model.ProviderID
	}

	if event.ModelID == "" {
		event.ModelID = model.ID
	}

	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}

	switch event.Type {
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
		return provider.NewError(
			providerID,
			provider.ErrorCodeTimeout,
			"request deadline exceeded",
			err,
		)
	}

	var existing *provider.Error
	if errors.As(err, &existing) {
		return existing
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
		return provider.NewError(
			providerID,
			provider.ErrorCodeTimeout,
			"network request timed out",
			err,
		)
	}

	return provider.NewError(
		providerID,
		provider.ErrorCodeProvider,
		err.Error(),
		err,
	)
}
