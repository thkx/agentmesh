package modelgateway

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
)

type gatewayTestAdapter struct {
	response    *GenerateResponse
	generateErr error
	request     GenerateRequest
	stream      <-chan StreamEvent
	streamErr   error
}

func (a *gatewayTestAdapter) Generate(_ context.Context, req GenerateRequest) (*GenerateResponse, error) {
	a.request = req
	return a.response, a.generateErr
}

func (a *gatewayTestAdapter) Stream(context.Context, GenerateRequest) (<-chan StreamEvent, error) {
	return a.stream, a.streamErr
}

func newGatewayTestFixture(t *testing.T, model provider.Model, enabled bool, adapter Model) (*Gateway, *Registry) {
	t.Helper()
	providers := provider.NewProviderRegistry()
	p := provider.Provider{ID: model.ProviderID, Type: provider.TypeOpenAI, Name: "test", Kind: provider.KindCloud, Enabled: enabled}
	if err := providers.Register(p); err != nil {
		t.Fatal(err)
	}
	models, err := NewStaticResolver(model)
	if err != nil {
		t.Fatal(err)
	}
	adapters := NewRegistry()
	if adapter != nil {
		if err := adapters.Register(model.ProviderID, adapter); err != nil {
			t.Fatal(err)
		}
	}
	gateway, err := NewGatewayWithProviders(adapters, models, providers)
	if err != nil {
		t.Fatal(err)
	}
	return gateway, adapters
}

func validGatewayModel() provider.Model {
	return provider.Model{
		ID: "chat-fast", ProviderID: "openai-primary", Name: "gpt-test",
		MaxOutputTokens: 2048, SupportsStreaming: true, Enabled: true,
	}
}

func validGatewayRequest() GenerateRequest {
	return GenerateRequest{ModelID: "chat-fast", Messages: []Message{{Role: RoleUser, Content: "hello"}}}
}

func TestGatewayGenerateResolvesAndNormalizesProviderInstance(t *testing.T) {
	adapter := &gatewayTestAdapter{response: &GenerateResponse{ProviderID: "openai", ModelID: "wrong", Model: "wrong"}}
	gateway, _ := newGatewayTestFixture(t, validGatewayModel(), true, adapter)

	got, err := gateway.Generate(context.Background(), validGatewayRequest())
	if err != nil {
		t.Fatal(err)
	}
	if adapter.request.ProviderID != "openai-primary" || adapter.request.ProviderModel != "gpt-test" {
		t.Fatalf("adapter received unresolved request: %+v", adapter.request)
	}
	if got.ProviderID != "openai-primary" {
		t.Fatalf("provider instance id not normalized: %q", got.ProviderID)
	}
	if got.ModelID != "chat-fast" || got.Model != "gpt-test" {
		t.Fatalf("model identity not normalized: %+v", got)
	}
}

func TestGatewayRejectsOutputTokensAboveModelLimit(t *testing.T) {
	adapter := &gatewayTestAdapter{response: &GenerateResponse{}}
	gateway, _ := newGatewayTestFixture(t, validGatewayModel(), true, adapter)
	req := validGatewayRequest()
	req.MaxOutputTokens = 2049

	_, err := gateway.Generate(context.Background(), req)
	if !errors.Is(err, ErrOutputTokenLimitExceeded) {
		t.Fatalf("expected token limit error, got %v", err)
	}
	if adapter.request.ModelID != "" {
		t.Fatal("adapter must not be called for invalid token limit")
	}
}

func TestGatewayRejectsDisabledProvider(t *testing.T) {
	adapter := &gatewayTestAdapter{response: &GenerateResponse{}}
	gateway, _ := newGatewayTestFixture(t, validGatewayModel(), false, adapter)
	_, err := gateway.Generate(context.Background(), validGatewayRequest())
	if !errors.Is(err, provider.ErrProviderDisabled) {
		t.Fatalf("expected disabled provider error, got %v", err)
	}
}

func TestGatewayRejectsDisabledModel(t *testing.T) {
	model := validGatewayModel()
	model.Enabled = false
	adapter := &gatewayTestAdapter{response: &GenerateResponse{}}
	gateway, _ := newGatewayTestFixture(t, model, true, adapter)
	_, err := gateway.Generate(context.Background(), validGatewayRequest())
	if !errors.Is(err, provider.ErrModelDisabled) {
		t.Fatalf("expected disabled model error, got %v", err)
	}
}

func TestGatewayNormalizesProviderErrors(t *testing.T) {
	adapter := &gatewayTestAdapter{generateErr: fmt.Errorf("upstream: %w", context.DeadlineExceeded)}
	gateway, _ := newGatewayTestFixture(t, validGatewayModel(), true, adapter)
	_, err := gateway.Generate(context.Background(), validGatewayRequest())
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected normalized provider error, got %T %v", err, err)
	}
	if providerErr.ProviderID != "openai-primary" || providerErr.Code != provider.ErrorCodeTimeout {
		t.Fatalf("unexpected normalized error: %+v", providerErr)
	}
}

func TestGatewayStreamNormalizesEventsAndStopsAtCompletion(t *testing.T) {
	source := make(chan StreamEvent, 3)
	source <- NewTextDeltaEvent("openai", "upstream-model", "hello")
	source <- NewCompletedEvent(&GenerateResponse{ProviderID: "openai"})
	source <- NewTextDeltaEvent("openai", "upstream-model", "must-not-pass")
	close(source)
	adapter := &gatewayTestAdapter{stream: source}
	gateway, _ := newGatewayTestFixture(t, validGatewayModel(), true, adapter)

	out, err := gateway.Stream(context.Background(), validGatewayRequest())
	if err != nil {
		t.Fatal(err)
	}
	var events []StreamEvent
	for event := range out {
		events = append(events, event)
	}
	if len(events) != 2 {
		t.Fatalf("expected delta and completed events only, got %d", len(events))
	}
	for _, event := range events {
		if event.ProviderID != "openai-primary" || event.ModelID != "chat-fast" {
			t.Fatalf("event identity not normalized: %+v", event)
		}
	}
	if events[1].Response == nil || events[1].Response.ProviderID != "openai-primary" {
		t.Fatalf("completed response not normalized: %+v", events[1])
	}
}

func TestGatewayStreamRejectsUnsupportedModelCapability(t *testing.T) {
	model := validGatewayModel()
	model.SupportsStreaming = false
	adapter := &gatewayTestAdapter{stream: make(chan StreamEvent)}
	gateway, _ := newGatewayTestFixture(t, model, true, adapter)
	_, err := gateway.Stream(context.Background(), validGatewayRequest())
	if !errors.Is(err, ErrStreamingDisabled) {
		t.Fatalf("expected streaming disabled, got %v", err)
	}
}

func TestGatewayStreamConvertsInvalidEventToError(t *testing.T) {
	source := make(chan StreamEvent, 1)
	source <- StreamEvent{Type: "unexpected"}
	close(source)
	adapter := &gatewayTestAdapter{stream: source}
	gateway, _ := newGatewayTestFixture(t, validGatewayModel(), true, adapter)
	out, err := gateway.Stream(context.Background(), validGatewayRequest())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-out:
		if event.Type != StreamEventError || event.Error == nil {
			t.Fatalf("expected normalized stream error, got %+v", event)
		}
		if !errors.Is(event.Error, ErrInvalidStreamEvent) {
			t.Fatalf("expected invalid event cause, got %v", event.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream error")
	}
}

func TestGatewayRejectsNilResponse(t *testing.T) {
	adapter := &gatewayTestAdapter{}
	gateway, _ := newGatewayTestFixture(t, validGatewayModel(), true, adapter)
	_, err := gateway.Generate(context.Background(), validGatewayRequest())
	if !errors.Is(err, ErrNilResponse) {
		t.Fatalf("expected nil response error, got %v", err)
	}
}

func TestNormalizeErrorRewritesProviderInstanceID(t *testing.T) {
	original := provider.NewError("openai", provider.ErrorCodeRateLimit, "limited", nil)
	normalized := NormalizeError("openai-backup", original)
	var got *provider.Error
	if !errors.As(normalized, &got) {
		t.Fatalf("expected provider error, got %T", normalized)
	}
	if got.ProviderID != "openai-backup" || got.Code != provider.ErrorCodeRateLimit {
		t.Fatalf("unexpected normalized error: %+v", got)
	}
}

func TestGenerateRequestRejectsNonFiniteSamplingParameters(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		req := validGatewayRequest()
		req.Temperature = &value
		if err := req.Validate(); !errors.Is(err, ErrInvalidTemperature) {
			t.Fatalf("expected invalid temperature for %v, got %v", value, err)
		}
		req = validGatewayRequest()
		req.TopP = &value
		if err := req.Validate(); !errors.Is(err, ErrInvalidTopP) {
			t.Fatalf("expected invalid top_p for %v, got %v", value, err)
		}
	}
}

func TestGatewayStreamStopsAfterContextCancellation(t *testing.T) {
	source := make(chan StreamEvent)
	adapter := &gatewayTestAdapter{stream: source}
	gateway, _ := newGatewayTestFixture(t, validGatewayModel(), true, adapter)
	ctx, cancel := context.WithCancel(context.Background())
	out, err := gateway.Stream(ctx, validGatewayRequest())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case _, ok := <-out:
		if ok {
			// A provider event was not sent, so any value is unexpected.
			t.Fatal("received an event after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("gateway stream did not close after cancellation")
	}
}

func TestGatewayReportsMissingCloudCredential(t *testing.T) {
	models := provider.NewModelRegistry()
	if err := models.Register(provider.Model{ID: "chat-fast", ProviderID: "openai-primary", Name: "gpt-4.1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	providers := provider.NewProviderRegistry()
	if err := providers.Register(provider.Provider{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "Primary", Kind: provider.KindCloud, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	gateway, err := NewGatewayWithProviders(NewRegistry(), models, providers)
	if err != nil {
		t.Fatal(err)
	}
	_, err = gateway.Generate(context.Background(), GenerateRequest{ModelID: "chat-fast", Messages: []Message{{Role: "user", Content: "hello"}}})
	if !errors.Is(err, appcredential.ErrCredentialUnavailable) {
		t.Fatalf("expected missing-credential error, got %v", err)
	}
}
