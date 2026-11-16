package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// UsageHandler exposes read-only aggregates and explicit operator pricing updates.
type UsageHandler struct{ db *sql.DB }

func NewUsageHandler(db *sql.DB) *UsageHandler { return &UsageHandler{db: db} }
func (h *UsageHandler) RegisterRoutes(r gin.IRoutes) {
	r.GET("/usage/summary", h.summary)
	r.GET("/usage/by-model", h.byModel)
	r.GET("/usage/events", h.events)
	r.GET("/usage/rates", h.listRates)
	r.POST("/usage/rates", h.setRate)
}
func (h *UsageHandler) summary(c *gin.Context) {
	since := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if raw := c.Query("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "since must be RFC3339"})
			return
		}
		since = parsed
	}
	var out struct {
		Requests         int64 `json:"requests"`
		InputTokens      int64 `json:"input_tokens"`
		OutputTokens     int64 `json:"output_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
		UnavailableUsage int64 `json:"unavailable_usage"`
	}
	err := h.db.QueryRowContext(c.Request.Context(), `SELECT COUNT(*),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(total_tokens),0),COALESCE(SUM(CASE WHEN usage_source='unavailable' THEN 1 ELSE 0 END),0) FROM model_usage_events WHERE occurred_at>=?`, since.UTC().Format(time.RFC3339Nano)).Scan(&out.Requests, &out.InputTokens, &out.OutputTokens, &out.TotalTokens, &out.UnavailableUsage)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "usage summary unavailable"})
		return
	}
	costRows, costErr := h.db.QueryContext(c.Request.Context(), "SELECT currency,COALESCE(SUM(estimated_cost_micros),0) FROM model_usage_events WHERE occurred_at>=? AND currency IS NOT NULL GROUP BY currency ORDER BY currency", since.UTC().Format(time.RFC3339Nano))
	if costErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "usage cost summary unavailable"})
		return
	}
	defer costRows.Close()
	costs := make([]gin.H, 0)
	for costRows.Next() {
		var currency string
		var amount int64
		if costRows.Scan(&currency, &amount) == nil {
			costs = append(costs, gin.H{"currency": currency, "estimated_cost_micros": amount})
		}
	}
	c.JSON(http.StatusOK, gin.H{"since": since.UTC(), "summary": out, "costs_by_currency": costs})
}
func (h *UsageHandler) byModel(c *gin.Context) {
	since := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if raw := c.Query("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "since must be RFC3339"})
			return
		}
		since = parsed
	}
	rows, err := h.db.QueryContext(c.Request.Context(), `SELECT provider_id,model_id,COALESCE(currency,''),COUNT(*),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(estimated_cost_micros),0) FROM model_usage_events WHERE occurred_at>=? GROUP BY provider_id,model_id,currency ORDER BY SUM(total_tokens) DESC LIMIT 200`, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "usage breakdown unavailable"})
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var p, m, currency string
		var n, in, out, cost int64
		if err = rows.Scan(&p, &m, &currency, &n, &in, &out, &cost); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "usage breakdown unavailable"})
			return
		}
		items = append(items, gin.H{"provider_id": p, "model_id": m, "currency": currency, "requests": n, "input_tokens": in, "output_tokens": out, "estimated_cost_micros": cost})
	}
	if err = rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "usage breakdown unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
func (h *UsageHandler) listRates(c *gin.Context) {
	rows, err := h.db.QueryContext(c.Request.Context(), `SELECT id,provider_id,model_id,currency,input_rate_micros_per_million,output_rate_micros_per_million,effective_at FROM model_pricing_rates ORDER BY provider_id,model_id,effective_at DESC LIMIT 500`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "pricing rates unavailable"})
		return
	}
	defer rows.Close()
	type rate struct {
		ID          string `json:"id"`
		ProviderID  string `json:"provider_id"`
		ModelID     string `json:"model_id"`
		Currency    string `json:"currency"`
		InputRate   int64  `json:"input_rate_micros_per_million"`
		OutputRate  int64  `json:"output_rate_micros_per_million"`
		EffectiveAt string `json:"effective_at"`
	}
	items := make([]rate, 0)
	for rows.Next() {
		var x rate
		if err = rows.Scan(&x.ID, &x.ProviderID, &x.ModelID, &x.Currency, &x.InputRate, &x.OutputRate, &x.EffectiveAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "pricing rates unavailable"})
			return
		}
		items = append(items, x)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
func (h *UsageHandler) setRate(c *gin.Context) {
	var in struct {
		ProviderID  string `json:"provider_id"`
		ModelID     string `json:"model_id"`
		Currency    string `json:"currency"`
		InputRate   int64  `json:"input_rate_micros_per_million"`
		OutputRate  int64  `json:"output_rate_micros_per_million"`
		EffectiveAt string `json:"effective_at"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid pricing rate"})
		return
	}
	in.ProviderID = strings.TrimSpace(in.ProviderID)
	in.ModelID = strings.TrimSpace(in.ModelID)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.ProviderID == "" || in.ModelID == "" || len(in.Currency) != 3 || in.InputRate < 0 || in.OutputRate < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider, model, three-letter currency and non-negative rates are required"})
		return
	}
	effective := time.Now().UTC()
	if in.EffectiveAt != "" {
		parsed, err := time.Parse(time.RFC3339, in.EffectiveAt)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "effective_at must be RFC3339"})
			return
		}
		effective = parsed.UTC()
	}
	_, err := h.db.ExecContext(c.Request.Context(), `INSERT INTO model_pricing_rates(id,provider_id,model_id,currency,input_rate_micros_per_million,output_rate_micros_per_million,effective_at,created_at) VALUES(?,?,?,?,?,?,?,?)`, uuid.NewString(), in.ProviderID, in.ModelID, in.Currency, in.InputRate, in.OutputRate, effective.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not save pricing rate"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"status": "created"})
}

func (h *UsageHandler) events(c *gin.Context) {
	parseTime := func(name string) (time.Time, bool) {
		raw := strings.TrimSpace(c.Query(name))
		if raw == "" {
			return time.Time{}, true
		}
		value, err := time.Parse(time.RFC3339, raw)
		return value.UTC(), err == nil
	}
	since, ok := parseTime("since")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "since must be RFC3339"})
		return
	}
	until, ok := parseTime("until")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "until must be RFC3339"})
		return
	}
	if since.IsZero() {
		since = time.Now().UTC().Add(-30 * 24 * time.Hour)
	}
	if !until.IsZero() && !until.After(since) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "until must be later than since"})
		return
	}
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 200"})
			return
		}
		limit = n
	}
	offset := 0
	if raw := c.Query("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 1000000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "offset must be between 0 and 1000000"})
			return
		}
		offset = n
	}
	where := []string{"occurred_at>=?"}
	args := []any{since.Format(time.RFC3339Nano)}
	if !until.IsZero() {
		where = append(where, "occurred_at<?")
		args = append(args, until.Format(time.RFC3339Nano))
	}
	for _, filter := range []struct{ name, column string }{{"provider_id", "provider_id"}, {"model_id", "model_id"}, {"execution_id", "execution_id"}, {"attempt_id", "attempt_id"}} {
		if value := strings.TrimSpace(c.Query(filter.name)); value != "" {
			if len(value) > 128 {
				c.JSON(http.StatusBadRequest, gin.H{"error": filter.name + " is too long"})
				return
			}
			where = append(where, filter.column+"=?")
			args = append(args, value)
		}
	}
	query := `SELECT id,occurred_at,provider_id,model_id,input_tokens,output_tokens,total_tokens,usage_source,currency,estimated_cost_micros,execution_id,attempt_id,request_id,trace_id FROM model_usage_events WHERE ` + strings.Join(where, " AND ") + ` ORDER BY occurred_at DESC,id DESC LIMIT ? OFFSET ?`
	args = append(args, limit+1, offset)
	rows, err := h.db.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "usage events unavailable"})
		return
	}
	defer rows.Close()
	type event struct {
		ID           string `json:"id"`
		OccurredAt   string `json:"occurred_at"`
		ProviderID   string `json:"provider_id"`
		ModelID      string `json:"model_id"`
		InputTokens  *int64 `json:"input_tokens,omitempty"`
		OutputTokens *int64 `json:"output_tokens,omitempty"`
		TotalTokens  *int64 `json:"total_tokens,omitempty"`
		Source       string `json:"usage_source"`
		Currency     string `json:"currency,omitempty"`
		CostMicros   *int64 `json:"estimated_cost_micros,omitempty"`
		ExecutionID  string `json:"execution_id,omitempty"`
		AttemptID    string `json:"attempt_id,omitempty"`
		RequestID    string `json:"request_id,omitempty"`
		TraceID      string `json:"trace_id,omitempty"`
	}
	items := make([]event, 0, limit+1)
	for rows.Next() {
		var item event
		var in, out, total, cost sql.NullInt64
		var currency, execution, attempt, request, trace sql.NullString
		if err = rows.Scan(&item.ID, &item.OccurredAt, &item.ProviderID, &item.ModelID, &in, &out, &total, &item.Source, &currency, &cost, &execution, &attempt, &request, &trace); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "usage events unavailable"})
			return
		}
		if in.Valid {
			item.InputTokens = &in.Int64
		}
		if out.Valid {
			item.OutputTokens = &out.Int64
		}
		if total.Valid {
			item.TotalTokens = &total.Int64
		}
		if cost.Valid {
			item.CostMicros = &cost.Int64
		}
		item.Currency = currency.String
		item.ExecutionID = execution.String
		item.AttemptID = attempt.String
		item.RequestID = request.String
		item.TraceID = trace.String
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "usage events unavailable"})
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "limit": limit, "offset": offset, "has_more": hasMore, "next_offset": offset + limit, "since": since, "until": until})
}
