package handlers

import (
    "database/sql"
    "net/http"
    "strings"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/google/uuid"
)

// UsageHandler exposes read-only aggregates and explicit operator pricing updates.
type UsageHandler struct { db *sql.DB }
func NewUsageHandler(db *sql.DB) *UsageHandler { return &UsageHandler{db:db} }
func (h *UsageHandler) RegisterRoutes(r gin.IRoutes) {
    r.GET("/usage/summary",h.summary)
    r.GET("/usage/by-model",h.byModel)
    r.GET("/usage/rates",h.listRates)
    r.POST("/usage/rates",h.setRate)
}
func (h *UsageHandler) summary(c *gin.Context) {
    since:=time.Now().UTC().Add(-30*24*time.Hour)
    if raw:=c.Query("since");raw!="" { parsed,err:=time.Parse(time.RFC3339,raw);if err!=nil { c.JSON(http.StatusBadRequest,gin.H{"error":"since must be RFC3339"});return };since=parsed }
    var out struct { Requests int64 `json:"requests"`; InputTokens int64 `json:"input_tokens"`; OutputTokens int64 `json:"output_tokens"`; TotalTokens int64 `json:"total_tokens"`; EstimatedCostMicros int64 `json:"estimated_cost_micros"`; UnavailableUsage int64 `json:"unavailable_usage"` }
    err:=h.db.QueryRowContext(c.Request.Context(),`SELECT COUNT(*),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(total_tokens),0),COALESCE(SUM(estimated_cost_micros),0),COALESCE(SUM(CASE WHEN usage_source='unavailable' THEN 1 ELSE 0 END),0) FROM model_usage_events WHERE occurred_at>=?`,since.UTC().Format(time.RFC3339Nano)).Scan(&out.Requests,&out.InputTokens,&out.OutputTokens,&out.TotalTokens,&out.EstimatedCostMicros,&out.UnavailableUsage)
    if err!=nil { c.JSON(http.StatusInternalServerError,gin.H{"error":"usage summary unavailable"});return }
    c.JSON(http.StatusOK,gin.H{"since":since.UTC(),"summary":out})
}
func (h *UsageHandler) byModel(c *gin.Context) {
    since:=time.Now().UTC().Add(-30*24*time.Hour)
    if raw:=c.Query("since");raw!="" { parsed,err:=time.Parse(time.RFC3339,raw);if err!=nil { c.JSON(http.StatusBadRequest,gin.H{"error":"since must be RFC3339"});return };since=parsed }
    rows,err:=h.db.QueryContext(c.Request.Context(),`SELECT provider_id,model_id,COUNT(*),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(estimated_cost_micros),0) FROM model_usage_events WHERE occurred_at>=? GROUP BY provider_id,model_id ORDER BY SUM(total_tokens) DESC LIMIT 200`,since.UTC().Format(time.RFC3339Nano))
    if err!=nil { c.JSON(http.StatusInternalServerError,gin.H{"error":"usage breakdown unavailable"});return };defer rows.Close()
    items:=make([]gin.H,0)
    for rows.Next(){var p,m string;var n,in,out,cost int64;if err=rows.Scan(&p,&m,&n,&in,&out,&cost);err!=nil{c.JSON(http.StatusInternalServerError,gin.H{"error":"usage breakdown unavailable"});return};items=append(items,gin.H{"provider_id":p,"model_id":m,"requests":n,"input_tokens":in,"output_tokens":out,"estimated_cost_micros":cost})}
    if err=rows.Err();err!=nil{c.JSON(http.StatusInternalServerError,gin.H{"error":"usage breakdown unavailable"});return};c.JSON(http.StatusOK,gin.H{"items":items})
}
func (h *UsageHandler) listRates(c *gin.Context) {
    rows,err:=h.db.QueryContext(c.Request.Context(),`SELECT id,provider_id,model_id,currency,input_rate_micros_per_million,output_rate_micros_per_million,effective_at FROM model_pricing_rates ORDER BY provider_id,model_id,effective_at DESC LIMIT 500`)
    if err!=nil{c.JSON(http.StatusInternalServerError,gin.H{"error":"pricing rates unavailable"});return};defer rows.Close()
    type rate struct { ID string `json:"id"`; ProviderID string `json:"provider_id"`; ModelID string `json:"model_id"`; Currency string `json:"currency"`; InputRate int64 `json:"input_rate_micros_per_million"`; OutputRate int64 `json:"output_rate_micros_per_million"`; EffectiveAt string `json:"effective_at"` }
    items:=make([]rate,0);for rows.Next(){var x rate;if err=rows.Scan(&x.ID,&x.ProviderID,&x.ModelID,&x.Currency,&x.InputRate,&x.OutputRate,&x.EffectiveAt);err!=nil{c.JSON(http.StatusInternalServerError,gin.H{"error":"pricing rates unavailable"});return};items=append(items,x)}
    c.JSON(http.StatusOK,gin.H{"items":items})
}
func (h *UsageHandler) setRate(c *gin.Context) {
    var in struct { ProviderID string `json:"provider_id"`; ModelID string `json:"model_id"`; Currency string `json:"currency"`; InputRate int64 `json:"input_rate_micros_per_million"`; OutputRate int64 `json:"output_rate_micros_per_million"`; EffectiveAt string `json:"effective_at"` }
    if err:=c.ShouldBindJSON(&in);err!=nil{c.JSON(http.StatusBadRequest,gin.H{"error":"invalid pricing rate"});return}
    in.ProviderID=strings.TrimSpace(in.ProviderID);in.ModelID=strings.TrimSpace(in.ModelID);in.Currency=strings.ToUpper(strings.TrimSpace(in.Currency))
    if in.ProviderID==""||in.ModelID==""||len(in.Currency)!=3||in.InputRate<0||in.OutputRate<0{c.JSON(http.StatusBadRequest,gin.H{"error":"provider, model, three-letter currency and non-negative rates are required"});return}
    effective:=time.Now().UTC();if in.EffectiveAt!=""{parsed,err:=time.Parse(time.RFC3339,in.EffectiveAt);if err!=nil{c.JSON(http.StatusBadRequest,gin.H{"error":"effective_at must be RFC3339"});return};effective=parsed.UTC()}
    _,err:=h.db.ExecContext(c.Request.Context(),`INSERT INTO model_pricing_rates(id,provider_id,model_id,currency,input_rate_micros_per_million,output_rate_micros_per_million,effective_at,created_at) VALUES(?,?,?,?,?,?,?,?)`,uuid.NewString(),in.ProviderID,in.ModelID,in.Currency,in.InputRate,in.OutputRate,effective.Format(time.RFC3339Nano),time.Now().UTC().Format(time.RFC3339Nano))
    if err!=nil{c.JSON(http.StatusInternalServerError,gin.H{"error":"could not save pricing rate"});return};c.JSON(http.StatusCreated,gin.H{"status":"created"})
}
