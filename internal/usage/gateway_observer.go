package usage

import (
	"context"
	"time"

	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"go.opentelemetry.io/otel/trace"
)

// GatewayObserver records exactly one terminal usage observation per Gateway request.
// Write failures are surfaced to operational metrics; they never fail the model response.
type GatewayObserver struct {
	Ledger  *Ledger
	OnError func()
}

func (o GatewayObserver) ObserveModelUsage(ctx context.Context, providerID provider.ID, modelID string, tokens modelgateway.Usage, occurredAt time.Time, requestKey string) {
	if o.Ledger == nil {
		return
	}
	source := "provider_reported"
	if tokens.InputTokens == 0 && tokens.OutputTokens == 0 && tokens.TotalTokens == 0 {
		source = "unavailable"
	}
	provenance := modelgateway.ExecutionProvenanceFromContext(ctx)
	traceID := ""
	if span := trace.SpanContextFromContext(ctx); span.IsValid() {
		traceID = span.TraceID().String()
	}
	err := o.Ledger.Record(ctx, Record{
		IdempotencyKey: requestKey,
		ProviderID: providerID,
		ModelID: modelID,
		OccurredAt: occurredAt,
		Usage: tokens,
		Source: source,
		RequestID: requestKey,
		TraceID: traceID,
		ExecutionID: provenance.ExecutionID,
		AttemptID: provenance.AttemptID,
	})
	if err != nil && o.OnError != nil {
		o.OnError()
	}
}
