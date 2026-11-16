package usage

import (
    "context"
    "database/sql"
    "testing"
    "time"

    _ "modernc.org/sqlite"
    "github.com/thkx/agentmesh/internal/domain/provider"
    "github.com/thkx/agentmesh/internal/modelgateway"
)
func TestLedgerIdempotencyAndPricingSnapshot(t *testing.T) {
    db,err:=sql.Open("sqlite",":memory:"); if err!=nil { t.Fatal(err) }; defer db.Close()
    schema:=`CREATE TABLE model_usage_events(id TEXT PRIMARY KEY,idempotency_key TEXT UNIQUE,provider_id TEXT,model_id TEXT,occurred_at TEXT,input_tokens INTEGER,output_tokens INTEGER,total_tokens INTEGER,cached_input_tokens INTEGER,reasoning_tokens INTEGER,usage_source TEXT,currency TEXT,input_rate_micros_per_million INTEGER,output_rate_micros_per_million INTEGER,estimated_cost_micros INTEGER,request_id TEXT,trace_id TEXT,execution_id TEXT,attempt_id TEXT,created_at TEXT);
    CREATE TABLE model_pricing_rates(id TEXT PRIMARY KEY,provider_id TEXT,model_id TEXT,currency TEXT,input_rate_micros_per_million INTEGER,output_rate_micros_per_million INTEGER,effective_at TEXT,created_at TEXT);`
    if _,err=db.Exec(schema);err!=nil { t.Fatal(err) }
    l,_:=NewLedger(db); now:=time.Now().UTC()
    rate:=Rate{ProviderID:provider.ID("openai"),ModelID:"gpt-test",Currency:"USD",InputRateMicrosPerMillion:1_000_000,OutputRateMicrosPerMillion:2_000_000,EffectiveAt:now.Add(-time.Hour)}
    if err=l.SetRate(context.Background(),rate);err!=nil { t.Fatal(err) }
    rec:=Record{IdempotencyKey:"request-1",ProviderID:rate.ProviderID,ModelID:rate.ModelID,OccurredAt:now,Usage:modelgateway.Usage{InputTokens:10,OutputTokens:5}}
    if err=l.Record(context.Background(),rec);err!=nil { t.Fatal(err) }
    if err=l.Record(context.Background(),rec);err!=nil { t.Fatal(err) }
    var count,cost int
    if err=db.QueryRow("SELECT COUNT(*),estimated_cost_micros FROM model_usage_events").Scan(&count,&cost);err!=nil { t.Fatal(err) }
    if count!=1||cost!=20 { t.Fatalf("count=%d cost=%d",count,cost) }
}
