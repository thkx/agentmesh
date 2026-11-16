package modelgateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"time"

	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
	ErrDynamicStreamingUnsupported    = errors.New("dynamic credential routing does not support streaming yet")
)

// DynamicModelResolver optionally resolves AI Resource model IDs without requiring
// a duplicate legacy provider/model catalog entry.
type DynamicModelResolver interface {
	ResolveModel(context.Context, string) (provider.Model, bool, error)
}

// DynamicStreamingRouter is implemented by dynamic credential pools that support SSE.
type DynamicStreamingRouter interface {
	Stream(context.Context, GenerateRequest) (<-chan StreamEvent, error)
}

// DynamicRouter routes configured model keys through the persisted AI resource pool.
type DynamicRouter interface {
	Supports(context.Context, string) (bool, error)
	Generate(context.Context, GenerateRequest) (*GenerateResponse, error)
}

type Observer interface {
	ObserveModelGateway(operation, outcome string, elapsed time.Duration)
}

type UsageObserver interface {
	ObserveModelUsage(context.Context, provider.ID, string, Usage, time.Time, string)
}

type executionProvenanceKey struct{}

type ExecutionProvenance struct {
	ExecutionID string
	AttemptID   string
}

// WithExecutionProvenance attaches durable execution identity to a model request context.
func WithExecutionProvenance(ctx context.Context, executionID, attemptID string) context.Context {
	return context.WithValue(ctx, executionProvenanceKey{}, ExecutionProvenance{ExecutionID: executionID, AttemptID: attemptID})
}

// ExecutionProvenanceFromContext returns explicit provenance without inspecting prompts or request payloads.
func ExecutionProvenanceFromContext(ctx context.Context) ExecutionProvenance {
	value, _ := ctx.Value(executionProvenanceKey{}).(ExecutionProvenance)
	return value
}

type Gateway struct {
	observer      Observer
	usageObserver UsageObserver
	registry      *Registry
	resolver      Resolver
	providers     *provider.ProviderRegistry
	dynamicRouter DynamicRouter
}

// SetObserver installs an optional bounded-cardinality operational observer.
func (g *Gateway) SetObserver(observer Observer) {
	g.observer = observer
}

// SetUsageObserver installs the durable usage ledger observer before serving requests.
func (g *Gateway) SetUsageObserver(observer UsageObserver) { g.usageObserver = observer }

// SetDynamicRouter installs the optional database-backed credential router.
// Bootstrap must call this before the gateway starts serving requests.
func (g *Gateway) SetDynamicRouter(router DynamicRouter) {
	g.dynamicRouter = router
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
func (g *Gateway) Generate(ctx context.Context, req GenerateRequest) (response *GenerateResponse, resultErr error) {
	started := time.Now()
	ctx, span := otel.Tracer("agentmesh/modelgateway").Start(ctx, "modelgateway.generate")
	defer func() {
		if resultErr != nil {
			span.SetStatus(codes.Error, "model request failed")
		}
		span.End()
	}()
	defer func() { g.observe("generate", started, resultErr) }()
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate request: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, NormalizeError("", err)
	}

	model, adapter, resolvedReq, err := g.resolve(ctx, req)
	if err == nil {
		span.SetAttributes(attribute.String("agentmesh.provider_id", string(model.ProviderID)), attribute.String("agentmesh.model_id", string(model.ID)))
	}
	if err != nil {
		return nil, err
	}
	if g.dynamicRouter != nil {
		supported, supportErr := g.dynamicRouter.Supports(ctx, resolvedReq.ProviderModel)
		if supportErr != nil {
			return nil, fmt.Errorf("resolve dynamic credential route: %w", supportErr)
		}
		if supported {
			resp, dynamicErr := g.dynamicRouter.Generate(ctx, resolvedReq)
			if dynamicErr != nil {
				return nil, NormalizeError(model.ProviderID, dynamicErr)
			}
			if resp == nil {
				return nil, ErrNilResponse
			}
			normalizeResponse(resp, model)
			g.observeUsage(ctx, resp, usageKey(resp.ID))
			return resp, nil
		}
	}
	if adapter == nil {
		return nil, fmt.Errorf("%w: provider %s", appcredential.ErrCredentialUnavailable, model.ProviderID)
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
	g.observeUsage(ctx, resp, usageKey(resp.ID))

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
func (g *Gateway) Stream(ctx context.Context, req GenerateRequest) (events <-chan StreamEvent, resultErr error) {
	started := time.Now()
	ctx, span := otel.Tracer("agentmesh/modelgateway").Start(ctx, "modelgateway.stream")
	defer func() {
		if resultErr != nil {
			span.SetStatus(codes.Error, "model stream failed")
		}
		span.End()
	}()
	defer func() { g.observe("stream_open", started, resultErr) }()
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
	if g.dynamicRouter != nil {
		supported, supportErr := g.dynamicRouter.Supports(ctx, resolvedReq.ProviderModel)
		if supportErr != nil {
			return nil, fmt.Errorf("resolve dynamic credential route: %w", supportErr)
		}
		if supported {
			if !model.SupportsStreaming {
				return nil, fmt.Errorf("%w: %s", ErrStreamingDisabled, model.ID)
			}
			streamRouter, ok := g.dynamicRouter.(DynamicStreamingRouter)
			if !ok {
				return nil, ErrDynamicStreamingUnsupported
			}
			source, streamErr := streamRouter.Stream(ctx, resolvedReq)
			if streamErr != nil {
				return nil, streamErr
			}
			if source == nil {
				return nil, ErrNilStream
			}
			output := make(chan StreamEvent)
			requestKey := usageKey("")
			go func() {
				streamStarted := time.Now()
				usageRecorded := false
				defer close(output)
				for {
					select {
					case <-ctx.Done():
						g.observe("stream", streamStarted, ctx.Err())
						return
					case event, open := <-source:
						if !open {
							g.observe("stream", streamStarted, errors.New("provider stream ended without completion"))
							return
						}
						normalizeStreamEvent(&event, model)
						if event.Type == StreamEventError {
							g.observe("stream", streamStarted, errors.New("provider stream failed"))
						}
						if event.Type == StreamEventCompleted {
							g.observe("stream", streamStarted, nil)
						}
						if !usageRecorded && event.Type == StreamEventUsage && event.Usage != nil {
							g.observeUsageValues(ctx, event.ProviderID, event.ModelID, *event.Usage, event.CreatedAt, requestKey)
							usageRecorded = true
						}
						if !usageRecorded && event.Type == StreamEventCompleted && event.Response != nil {
							g.observeUsage(ctx, event.Response, requestKey)
							usageRecorded = true
						}
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
	}
	if adapter == nil {
		return nil, fmt.Errorf("%w: provider %s", appcredential.ErrCredentialUnavailable, model.ProviderID)
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
	requestKey := usageKey("")

	go func() {
		streamStarted := time.Now()
		usageRecorded := false
		defer close(output)

		for {
			select {
			case <-ctx.Done():
				g.observe("stream", streamStarted, ctx.Err())
				return

			case event, ok := <-source:
				if !ok {
					g.observe("stream", streamStarted, errors.New("provider stream ended without completion"))
					return
				}

				normalizeStreamEvent(&event, model)
				if event.Type == StreamEventError {
					g.observe("stream", streamStarted, errors.New("provider stream failed"))
				}
				if event.Type == StreamEventCompleted {
					g.observe("stream", streamStarted, nil)
				}
				if !usageRecorded && event.Type == StreamEventUsage && event.Usage != nil {
					g.observeUsageValues(ctx, event.ProviderID, event.ModelID, *event.Usage, event.CreatedAt, requestKey)
					usageRecorded = true
				}
				if !usageRecorded && event.Type == StreamEventCompleted && event.Response != nil {
					g.observeUsage(ctx, event.Response, requestKey)
					usageRecorded = true
				}

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

func (g *Gateway) observeUsage(ctx context.Context, response *GenerateResponse, key string) {
	if response == nil {
		return
	}
	g.observeUsageValues(ctx, response.ProviderID, response.ModelID, response.Usage, response.CreatedAt, key)
}
func (g *Gateway) observeUsageValues(ctx context.Context, providerID provider.ID, modelID string, usage Usage, occurredAt time.Time, key string) {
	if g == nil || g.usageObserver == nil || providerID == "" || modelID == "" {
		return
	}
	usage.Normalize()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	g.usageObserver.ObserveModelUsage(ctx, providerID, modelID, usage, occurredAt, key)
}
func usageKey(existing string) string {
	if existing != "" {
		return existing
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("usage-%d", time.Now().UnixNano())
}

func (g *Gateway) observe(operation string, started time.Time, err error) {
	if g == nil || g.observer == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = "canceled"
		}
	}
	g.observer.ObserveModelGateway(operation, outcome, time.Since(started))
}

// SupportsNativeToolCalling reports whether the resolved adapter implements provider-native function calling.
func (g *Gateway) SupportsNativeToolCalling(ctx context.Context, modelID string) bool {
	_, adapter, resolvedReq, err := g.resolve(ctx, GenerateRequest{ModelID: modelID, Messages: []Message{{Role: RoleUser, Content: "capability probe"}}})
	if err != nil {
		return false
	}
	if g.dynamicRouter != nil {
		supported, supportErr := g.dynamicRouter.Supports(ctx, resolvedReq.ProviderModel)
		if supportErr != nil {
			return false
		}
		if supported {
			capability, ok := g.dynamicRouter.(interface {
				SupportsNativeToolCalling(context.Context, string) (bool, error)
			})
			if !ok {
				return false
			}
			enabled, capabilityErr := capability.SupportsNativeToolCalling(ctx, resolvedReq.ProviderModel)
			return capabilityErr == nil && enabled
		}
	}
	if adapter == nil {
		return false
	}
	capability, ok := adapter.(interface{ SupportsNativeToolCalling() bool })
	return ok && capability.SupportsNativeToolCalling()
}

func (g *Gateway) resolve(ctx context.Context, req GenerateRequest) (provider.Model, Model, GenerateRequest, error) {
	model, err := g.resolver.Resolve(ctx, req.ModelID)
	dynamicModel := false
	if err != nil {
		if resolver, ok := g.dynamicRouter.(DynamicModelResolver); ok {
			resolved, found, dynamicErr := resolver.ResolveModel(ctx, req.ModelID)
			if dynamicErr != nil {
				return provider.Model{}, nil, GenerateRequest{}, fmt.Errorf("resolve dynamic model: %w", dynamicErr)
			}
			if found {
				model = resolved
				dynamicModel = true
				err = nil
			}
		}
	}
	if err != nil {
		return provider.Model{}, nil, GenerateRequest{}, fmt.Errorf("resolve model: %w", err)
	}
	if err := model.Validate(); err != nil {
		return provider.Model{}, nil, GenerateRequest{}, fmt.Errorf("invalid resolved model: %w", err)
	}
	if !model.Enabled {
		return provider.Model{}, nil, GenerateRequest{}, fmt.Errorf("%w: %s", provider.ErrModelDisabled, model.ID)
	}
	if model.MaxOutputTokens > 0 && req.MaxOutputTokens > model.MaxOutputTokens {
		return provider.Model{}, nil, GenerateRequest{}, fmt.Errorf(
			"%w: requested=%d allowed=%d model=%s",
			ErrOutputTokenLimitExceeded, req.MaxOutputTokens, model.MaxOutputTokens, model.ID,
		)
	}

	resolvedReq := req
	resolvedReq.ProviderID = model.ProviderID
	resolvedReq.ProviderModel = model.Name

	// Dynamic models are backed by AI Resource credentials and must not be
	// coupled to the legacy ProviderRegistry or its single-key adapter.
	if dynamicModel {
		return model, nil, resolvedReq, nil
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
		if providerInstance != nil && providerInstance.Kind == provider.KindCloud && g.dynamicRouter != nil {
			adapter = nil
		} else if providerInstance != nil && providerInstance.Kind == provider.KindCloud {
			return provider.Model{}, nil, GenerateRequest{}, fmt.Errorf("%w: provider %s", appcredential.ErrCredentialUnavailable, model.ProviderID)
		} else {
			return provider.Model{}, nil, GenerateRequest{}, fmt.Errorf("resolve provider adapter: %w", err)
		}
	}
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
