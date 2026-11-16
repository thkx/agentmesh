package usage

import (
    "context"
    "time"
    "github.com/thkx/agentmesh/internal/domain/provider"
    "github.com/thkx/agentmesh/internal/modelgateway"
)
type GatewayObserver struct { Ledger *Ledger }
func (o GatewayObserver) ObserveModelUsage(ctx context.Context, providerID provider.ID, modelID string, tokens modelgateway.Usage, occurredAt time.Time, requestKey string) {
    if o.Ledger == nil { return }
    _ = o.Ledger.Record(ctx, Record{IdempotencyKey:requestKey,ProviderID:providerID,ModelID:modelID,OccurredAt:occurredAt,Usage:tokens,Source:"provider_reported",RequestID:requestKey})
}
