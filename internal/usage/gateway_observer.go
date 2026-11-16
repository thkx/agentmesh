package usage

import (
	"context"
	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"time"
)

type GatewayObserver struct{ Ledger *Ledger }

func (o GatewayObserver) ObserveModelUsage(ctx context.Context, providerID provider.ID, modelID string, tokens modelgateway.Usage, occurredAt time.Time, requestKey string) {
	if o.Ledger == nil {
		return
	}
	source := "provider_reported"
	if tokens.InputTokens == 0 && tokens.OutputTokens == 0 && tokens.TotalTokens == 0 {
		source = "unavailable"
	}
	_ = o.Ledger.Record(ctx, Record{IdempotencyKey: requestKey, ProviderID: providerID, ModelID: modelID, OccurredAt: occurredAt, Usage: tokens, Source: source, RequestID: requestKey})
}
