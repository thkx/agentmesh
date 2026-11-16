// Package usage records provider-reported model token usage and immutable cost snapshots.
package usage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

var ErrInvalidPricing = errors.New("invalid model pricing rate")

type Record struct {
	IdempotencyKey string
	ProviderID     provider.ID
	ModelID        string
	OccurredAt     time.Time
	Usage          modelgateway.Usage
	Source         string
	RequestID      string
	TraceID        string
	ExecutionID    string
	AttemptID      string
}
type Rate struct {
	ID                         string
	ProviderID                 provider.ID
	ModelID                    string
	Currency                   string
	InputRateMicrosPerMillion  int64
	OutputRateMicrosPerMillion int64
	EffectiveAt                time.Time
}
type Ledger struct{ db *sql.DB }

func NewLedger(db *sql.DB) (*Ledger, error) {
	if db == nil {
		return nil, errors.New("usage ledger database is nil")
	}
	return &Ledger{db: db}, nil
}
func (l *Ledger) SetRate(ctx context.Context, r Rate) error {
	if l == nil || l.db == nil {
		return errors.New("usage ledger is not initialized")
	}
	r.Currency = strings.ToUpper(strings.TrimSpace(r.Currency))
	if r.ProviderID == "" || r.ModelID == "" || !validCurrencyCode(r.Currency) || r.InputRateMicrosPerMillion < 0 || r.OutputRateMicrosPerMillion < 0 {
		return ErrInvalidPricing
	}
	if r.EffectiveAt.IsZero() {
		r.EffectiveAt = time.Now().UTC()
	}
	if r.ID == "" {
		r.ID = randomID()
	}
	_, err := l.db.ExecContext(ctx, `INSERT INTO model_pricing_rates(id,provider_id,model_id,currency,input_rate_micros_per_million,output_rate_micros_per_million,effective_at,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		r.ID, r.ProviderID, r.ModelID, r.Currency, r.InputRateMicrosPerMillion, r.OutputRateMicrosPerMillion, r.EffectiveAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (l *Ledger) Record(ctx context.Context, r Record) error {
	if l == nil || l.db == nil {
		return errors.New("usage ledger is not initialized")
	}
	if r.IdempotencyKey == "" || r.ProviderID == "" || r.ModelID == "" {
		return errors.New("usage record requires idempotency key, provider and model")
	}
	if r.OccurredAt.IsZero() {
		r.OccurredAt = time.Now().UTC()
	}
	r.Usage.Normalize()
	if r.Source == "" {
		r.Source = "provider_reported"
	}
	if r.Source != "provider_reported" && r.Source != "estimated" && r.Source != "unavailable" {
		return errors.New("invalid usage source")
	}
	var currency any
	var inputRate, outputRate any
	var cost any
	if r.Source != "unavailable" {
		var c string
		var inRate, outRate int64
		err := l.db.QueryRowContext(ctx, `SELECT currency,input_rate_micros_per_million,output_rate_micros_per_million FROM model_pricing_rates WHERE provider_id=? AND model_id=? AND effective_at<=? ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1`, r.ProviderID, r.ModelID, r.OccurredAt.UTC().Format(time.RFC3339Nano)).Scan(&c, &inRate, &outRate)
		if err == nil {
			computed, ok := estimateCost(r.Usage.InputTokens, inRate, r.Usage.OutputTokens, outRate)
			if !ok {
				return errors.New("estimated usage cost overflow")
			}
			currency, inputRate, outputRate, cost = c, inRate, outRate, computed
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	_, err := l.db.ExecContext(ctx, `INSERT OR IGNORE INTO model_usage_events(id,idempotency_key,provider_id,model_id,occurred_at,input_tokens,output_tokens,total_tokens,cached_input_tokens,reasoning_tokens,usage_source,currency,input_rate_micros_per_million,output_rate_micros_per_million,estimated_cost_micros,request_id,trace_id,execution_id,attempt_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		randomID(), r.IdempotencyKey, r.ProviderID, r.ModelID, r.OccurredAt.UTC().Format(time.RFC3339Nano), nullableTokens(r.Source, r.Usage.InputTokens), nullableTokens(r.Source, r.Usage.OutputTokens), nullableTokens(r.Source, r.Usage.TotalTokens), nullableTokens(r.Source, r.Usage.CachedInputTokens), nullableTokens(r.Source, r.Usage.ReasoningTokens), r.Source, currency, inputRate, outputRate, cost, nullString(r.RequestID), nullString(r.TraceID), nullString(r.ExecutionID), nullString(r.AttemptID), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (l *Ledger) Summary(ctx context.Context, since time.Time) (Summary, error) {
	var s Summary
	err := l.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(total_tokens),0),COALESCE(SUM(estimated_cost_micros),0),SUM(CASE WHEN usage_source='unavailable' THEN 1 ELSE 0 END) FROM model_usage_events WHERE occurred_at>=?`, since.UTC().Format(time.RFC3339Nano)).Scan(&s.Requests, &s.InputTokens, &s.OutputTokens, &s.TotalTokens, &s.EstimatedCostMicros, &s.UnavailableUsage)
	return s, err
}

type Summary struct {
	Requests            int64
	InputTokens         int64
	OutputTokens        int64
	TotalTokens         int64
	EstimatedCostMicros int64
	UnavailableUsage    int64
}

func validCurrencyCode(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func estimateCost(input, inRate, output, outRate int64) (int64, bool) {
	if input < 0 || output < 0 || inRate < 0 || outRate < 0 {
		return 0, false
	}
	if input != 0 && inRate > math.MaxInt64/input {
		return 0, false
	}
	a := input * inRate / 1_000_000
	if output != 0 && outRate > math.MaxInt64/output {
		return 0, false
	}
	b := output * outRate / 1_000_000
	if b > math.MaxInt64-a {
		return 0, false
	}
	return a + b, true
}
func nullableTokens(source string, v int64) any {
	if source == "unavailable" {
		return nil
	}
	return v
}
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
