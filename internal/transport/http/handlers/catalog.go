package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

type CatalogHandler struct {
	// writeMu serializes catalog mutations so snapshot persistence and in-memory
	// registry publication cannot overwrite concurrent updates.
	writeMu   sync.Mutex
	providers *provider.ProviderRegistry
	models    *provider.ModelRegistry
	adapters  *modelgateway.Registry
	runtime   *modelgateway.ProviderRuntime
	repo      provider.CatalogRepository
	gateway   *modelgateway.Gateway
}

func NewCatalogHandler(pr *provider.ProviderRegistry, mr *provider.ModelRegistry, ar *modelgateway.Registry, runtime *modelgateway.ProviderRuntime, repo provider.CatalogRepository, gateway *modelgateway.Gateway) *CatalogHandler {
	return &CatalogHandler{providers: pr, models: mr, adapters: ar, runtime: runtime, repo: repo, gateway: gateway}
}

func (h *CatalogHandler) RegisterRoutes(r *gin.RouterGroup) {
	p := r.Group("/providers")
	p.GET("", h.listProviders)
	p.POST("", h.createProvider)
	p.GET("/:id", h.getProvider)
	p.PUT("/:id", h.updateProvider)
	p.PATCH("/:id/enabled", h.setProviderEnabled)
	p.DELETE("/:id", h.deleteProvider)
	p.POST("/:id/test", h.testProvider)
	m := r.Group("/models")
	m.GET("", h.listModels)
	m.POST("", h.createModel)
	m.GET("/:id", h.getModel)
	m.PUT("/:id", h.updateModel)
	m.PATCH("/:id/enabled", h.setModelEnabled)
	m.DELETE("/:id", h.deleteModel)
	r.POST("/generate", h.generate)
	r.POST("/generate/stream", h.stream)
}

type providerInput struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Kind          string `json:"kind"`
	BaseURL       string `json:"base_url"`
	CredentialRef string `json:"api_key_env"`
	Enabled       *bool  `json:"enabled"`
}
type providerAPIResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Kind      string    `json:"kind"`
	BaseURL   string    `json:"base_url,omitempty"`
	Enabled   bool      `json:"enabled"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type modelAPIResponse struct {
	ID                string    `json:"id"`
	ProviderID        string    `json:"provider_id"`
	Name              string    `json:"name"`
	DisplayName       string    `json:"display_name,omitempty"`
	ContextWindow     int       `json:"context_window,omitempty"`
	MaxOutputTokens   int       `json:"max_output_tokens,omitempty"`
	SupportsStreaming bool      `json:"supports_streaming"`
	Enabled           bool      `json:"enabled"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

func toProviderAPI(p provider.Provider) providerAPIResponse {
	status := "disabled"
	if p.Enabled {
		status = "enabled"
	}
	return providerAPIResponse{ID: string(p.ID), Name: p.Name, Type: string(p.Type), Kind: string(p.Kind), BaseURL: p.BaseURL, Enabled: p.Enabled, Status: status, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}
func toModelAPI(m provider.Model) modelAPIResponse {
	status := "disabled"
	if m.Enabled {
		status = "enabled"
	}
	return modelAPIResponse{ID: m.ID, ProviderID: string(m.ProviderID), Name: m.Name, DisplayName: m.DisplayName, ContextWindow: m.ContextWindow, MaxOutputTokens: m.MaxOutputTokens, SupportsStreaming: m.SupportsStreaming, Enabled: m.Enabled, Status: status, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}

type modelInput struct {
	ID                string `json:"id"`
	ProviderID        string `json:"provider_id"`
	Name              string `json:"name"`
	DisplayName       string `json:"display_name"`
	ContextWindow     int    `json:"context_window"`
	MaxOutputTokens   int    `json:"max_output_tokens"`
	SupportsStreaming bool   `json:"supports_streaming"`
	Enabled           *bool  `json:"enabled"`
}

func (h *CatalogHandler) listProviders(c *gin.Context) {
	items := h.providers.List()
	out := make([]providerAPIResponse, 0, len(items))
	for _, p := range items {
		out = append(out, toProviderAPI(p))
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}
func (h *CatalogHandler) getProvider(c *gin.Context) {
	p, err := h.providers.Get(provider.ID(c.Param("id")))
	if err != nil {
		apiError(c, 404, "provider_not_found", "Provider does not exist")
		return
	}
	c.JSON(200, toProviderAPI(p))
}
func (h *CatalogHandler) createProvider(c *gin.Context) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	var in providerInput
	if !bind(c, &in) {
		return
	}
	if strings.TrimSpace(in.ID) == "" {
		apiError(c, 400, "invalid_provider", "id is required")
		return
	}
	if _, err := h.providers.Get(provider.ID(in.ID)); err == nil {
		apiError(c, 409, "provider_exists", "Provider already exists")
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	p := provider.Provider{ID: provider.ID(in.ID), Name: in.Name, Type: provider.Type(strings.ToLower(in.Type)), Kind: provider.Kind(strings.ToLower(in.Kind)), BaseURL: in.BaseURL, CredentialRef: in.CredentialRef, Enabled: enabled}
	if err := h.applyProvider(c, p); err != nil {
		return
	}
	p, _ = h.providers.Get(p.ID)
	c.JSON(http.StatusCreated, toProviderAPI(p))
}
func (h *CatalogHandler) updateProvider(c *gin.Context) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	id := provider.ID(c.Param("id"))
	old, err := h.providers.Get(id)
	if err != nil {
		apiError(c, 404, "provider_not_found", "Provider does not exist")
		return
	}
	var in providerInput
	if !bind(c, &in) {
		return
	}
	if in.ID != "" && in.ID != string(id) {
		apiError(c, 400, "immutable_provider_id", "Provider ID cannot be changed")
		return
	}
	enabled := old.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	p := provider.Provider{ID: id, Name: in.Name, Type: provider.Type(strings.ToLower(in.Type)), Kind: provider.Kind(strings.ToLower(in.Kind)), BaseURL: in.BaseURL, CredentialRef: in.CredentialRef, Enabled: enabled, CreatedAt: old.CreatedAt}
	if p.Name == "" {
		p.Name = old.Name
	}
	if p.Type == "" {
		p.Type = old.Type
	}
	if p.Kind == "" {
		p.Kind = old.Kind
	}
	if p.BaseURL == "" {
		p.BaseURL = old.BaseURL
	}
	if p.CredentialRef == "" {
		p.CredentialRef = old.CredentialRef
	}
	if err := h.applyProvider(c, p); err != nil {
		return
	}
	p, _ = h.providers.Get(p.ID)
	c.JSON(200, toProviderAPI(p))
}
func (h *CatalogHandler) setProviderEnabled(c *gin.Context) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !bind(c, &in) {
		return
	}
	if in.Enabled == nil {
		apiError(c, 400, "invalid_request", "enabled is required")
		return
	}
	p, err := h.providers.Get(provider.ID(c.Param("id")))
	if err != nil {
		apiError(c, 404, "provider_not_found", "Provider does not exist")
		return
	}
	p.Enabled = *in.Enabled
	if err := h.applyProvider(c, p); err != nil {
		return
	}
	p, _ = h.providers.Get(p.ID)
	c.JSON(200, toProviderAPI(p))
}
func (h *CatalogHandler) applyProvider(c *gin.Context, p provider.Provider) error {
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if err := p.Validate(); err != nil {
		apiError(c, 400, "invalid_provider", err.Error())
		return err
	}
	oldProviders := h.providers.List()
	newProviders := replaceProvider(oldProviders, p)
	models := h.models.List()
	// Persist first. ProviderRuntime builds the new adapter before publishing it,
	// so a factory/credential error leaves the live runtime untouched.
	if err := h.repo.Sync(c.Request.Context(), newProviders, models); err != nil {
		apiError(c, 409, "catalog_persist_failed", "Could not persist provider configuration")
		return err
	}
	if err := h.runtime.ApplyProvider(p); err != nil {
		var rollbackErr error
		existed := false
		for _, old := range oldProviders {
			if old.ID == p.ID {
				existed = true
				break
			}
		}
		if existed {
			rollbackErr = h.repo.Sync(c.Request.Context(), oldProviders, models)
		} else {
			rollbackErr = h.repo.DeleteProvider(c.Request.Context(), p.ID)
		}
		if rollbackErr != nil {
			apiError(c, 500, "catalog_rollback_failed", "Provider runtime update failed and catalog rollback also failed")
			return errors.Join(err, rollbackErr)
		}
		apiError(c, 400, "provider_runtime_invalid", safeMessage(err))
		return err
	}
	return nil
}

func replaceProvider(items []provider.Provider, p provider.Provider) []provider.Provider {
	out := make([]provider.Provider, 0, len(items)+1)
	for _, item := range items {
		if item.ID != p.ID {
			out = append(out, item)
		}
	}
	return append(out, p)
}
func (h *CatalogHandler) deleteProvider(c *gin.Context) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	id := provider.ID(c.Param("id"))
	if _, err := h.providers.Get(id); err != nil {
		apiError(c, 404, "provider_not_found", "Provider does not exist")
		return
	}
	if err := h.repo.DeleteProvider(c.Request.Context(), id); err != nil {
		apiError(c, 409, "provider_in_use", "Provider cannot be deleted while models reference it")
		return
	}
	if err := h.runtime.RemoveProvider(id); err != nil {
		apiError(c, 500, "runtime_update_failed", "Provider was deleted but runtime update failed")
		return
	}
	c.Status(http.StatusNoContent)
}
func (h *CatalogHandler) testProvider(c *gin.Context) {
	id := provider.ID(c.Param("id"))
	p, err := h.providers.Get(id)
	if err != nil {
		apiError(c, 404, "provider_not_found", "Provider does not exist")
		return
	}
	if !p.Enabled {
		apiError(c, 409, "provider_disabled", "Provider is disabled")
		return
	}
	if !h.adapters.Has(id) {
		apiError(c, 503, "provider_not_ready", "Provider adapter is not initialized; configure credentials first")
		return
	}
	var selected *provider.Model
	for _, item := range h.models.List() {
		if item.ProviderID == id && item.Enabled {
			m := item
			selected = &m
			break
		}
	}
	if selected == nil {
		apiError(c, 409, "provider_model_missing", "Provider has no enabled model for connectivity testing")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	started := time.Now()
	_, err = h.gateway.Generate(ctx, modelgateway.GenerateRequest{
		ModelID:         selected.ID,
		Messages:        []modelgateway.Message{{Role: modelgateway.RoleUser, Content: "Reply with OK."}},
		MaxOutputTokens: 1,
	})
	latency := time.Since(started).Milliseconds()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			c.JSON(http.StatusGatewayTimeout, gin.H{"success": false, "code": "provider_test_timeout", "message": "Provider connectivity test timed out", "latency_ms": latency})
			return
		}
		// Never return raw upstream errors: SDK errors may contain request details.
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "code": "provider_test_failed", "message": "Provider connectivity test failed; check provider credentials and endpoint", "latency_ms": latency})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Provider connectivity test successful", "latency_ms": latency})
}

func (h *CatalogHandler) listModels(c *gin.Context) {
	items := h.models.List()
	out := make([]modelAPIResponse, 0, len(items))
	for _, m := range items {
		out = append(out, toModelAPI(m))
	}
	c.JSON(200, gin.H{"data": out})
}
func (h *CatalogHandler) getModel(c *gin.Context) {
	m, ok := h.models.Get(c.Param("id"))
	if !ok {
		apiError(c, 404, "model_not_found", "Model does not exist")
		return
	}
	c.JSON(200, toModelAPI(m))
}
func (h *CatalogHandler) createModel(c *gin.Context) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	var in modelInput
	if !bind(c, &in) {
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	m := provider.Model{ID: in.ID, ProviderID: provider.ID(in.ProviderID), Name: in.Name, DisplayName: in.DisplayName, ContextWindow: in.ContextWindow, MaxOutputTokens: in.MaxOutputTokens, SupportsStreaming: in.SupportsStreaming, Enabled: enabled}
	if _, err := h.providers.Get(m.ProviderID); err != nil {
		apiError(c, 400, "provider_not_found", "Referenced provider does not exist")
		return
	}
	if _, ok := h.models.Get(m.ID); ok {
		apiError(c, 409, "model_exists", "Model already exists")
		return
	}
	if err := h.applyModel(c, m); err != nil {
		return
	}
	m, _ = h.models.Get(m.ID)
	c.JSON(201, toModelAPI(m))
}
func (h *CatalogHandler) updateModel(c *gin.Context) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	id := c.Param("id")
	old, ok := h.models.Get(id)
	if !ok {
		apiError(c, 404, "model_not_found", "Model does not exist")
		return
	}
	var in modelInput
	if !bind(c, &in) {
		return
	}
	if in.ID != "" && in.ID != id {
		apiError(c, 400, "immutable_model_id", "Model ID cannot be changed")
		return
	}
	m := provider.Model{ID: id, ProviderID: old.ProviderID, Name: in.Name, DisplayName: in.DisplayName, ContextWindow: in.ContextWindow, MaxOutputTokens: in.MaxOutputTokens, SupportsStreaming: in.SupportsStreaming, Enabled: old.Enabled, CreatedAt: old.CreatedAt}
	if in.ProviderID != "" {
		m.ProviderID = provider.ID(in.ProviderID)
	}
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	}
	if m.Name == "" {
		m.Name = old.Name
	}
	if m.DisplayName == "" {
		m.DisplayName = old.DisplayName
	}
	if m.ContextWindow == 0 {
		m.ContextWindow = old.ContextWindow
	}
	if m.MaxOutputTokens == 0 {
		m.MaxOutputTokens = old.MaxOutputTokens
	}
	if _, err := h.providers.Get(m.ProviderID); err != nil {
		apiError(c, 400, "provider_not_found", "Referenced provider does not exist")
		return
	}
	if err := h.applyModel(c, m); err != nil {
		return
	}
	m, _ = h.models.Get(m.ID)
	c.JSON(200, toModelAPI(m))
}
func (h *CatalogHandler) setModelEnabled(c *gin.Context) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !bind(c, &in) {
		return
	}
	if in.Enabled == nil {
		apiError(c, 400, "invalid_request", "enabled is required")
		return
	}
	m, ok := h.models.Get(c.Param("id"))
	if !ok {
		apiError(c, 404, "model_not_found", "Model does not exist")
		return
	}
	m.Enabled = *in.Enabled
	if err := h.applyModel(c, m); err != nil {
		return
	}
	m, _ = h.models.Get(m.ID)
	c.JSON(200, toModelAPI(m))
}
func (h *CatalogHandler) applyModel(c *gin.Context, m provider.Model) error {
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	if err := m.Validate(); err != nil {
		apiError(c, 400, "invalid_model", err.Error())
		return err
	}
	if err := h.repo.Sync(c.Request.Context(), h.providers.List(), replaceModel(h.models.List(), m)); err != nil {
		apiError(c, 409, "catalog_persist_failed", "Could not persist model configuration")
		return err
	}
	if err := h.models.Upsert(m); err != nil {
		apiError(c, 500, "runtime_update_failed", "Model persisted but runtime update failed")
		return err
	}
	return nil
}
func replaceModel(items []provider.Model, m provider.Model) []provider.Model {
	out := make([]provider.Model, 0, len(items)+1)
	for _, x := range items {
		if x.ID != m.ID {
			out = append(out, x)
		}
	}
	return append(out, m)
}
func (h *CatalogHandler) deleteModel(c *gin.Context) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	id := c.Param("id")
	if _, ok := h.models.Get(id); !ok {
		apiError(c, 404, "model_not_found", "Model does not exist")
		return
	}
	if err := h.repo.DeleteModel(c.Request.Context(), id); err != nil {
		apiError(c, 500, "catalog_persist_failed", "Could not delete model")
		return
	}
	if err := h.models.Delete(id); err != nil {
		apiError(c, 500, "runtime_update_failed", "Model deleted but runtime update failed")
		return
	}
	c.Status(204)
}

func (h *CatalogHandler) generate(c *gin.Context) {
	var req modelgateway.GenerateRequest
	if !bind(c, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultGenerateTimeout)
	defer cancel()
	resp, err := h.gateway.Generate(ctx, req)
	if err != nil {
		writeGatewayError(c, err)
		return
	}
	c.JSON(200, resp)
}

const DefaultGenerateTimeout = 120 * time.Second

func (h *CatalogHandler) stream(c *gin.Context) {
	var req modelgateway.GenerateRequest
	if !bind(c, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultGenerateTimeout)
	defer cancel()
	events, err := h.gateway.Stream(ctx, req)
	if err != nil {
		writeGatewayError(c, err)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		apiError(c, 500, "stream_unsupported", "Streaming is not supported by this HTTP server")
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-events:
			if !open {
				return
			}
			payload := gin.H{"type": ev.Type, "provider_id": ev.ProviderID, "model_id": ev.ModelID, "delta": ev.Delta, "usage": ev.Usage, "response": ev.Response, "created_at": ev.CreatedAt}
			if ev.Error != nil {
				payload["error"] = gin.H{"message": ev.Error.Error()}
			}
			if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: ", ev.Type); err != nil {
				return
			}
			enc := json.NewEncoder(c.Writer)
			if err := enc.Encode(payload); err != nil {
				return
			}
			if _, err := fmt.Fprint(c.Writer, "\n"); err != nil {
				return
			}
			flusher.Flush()
			if ev.Type == modelgateway.StreamEventCompleted || ev.Type == modelgateway.StreamEventError {
				return
			}
		}
	}
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		apiError(c, 400, "invalid_json", err.Error())
		return false
	}
	return true
}
func apiError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"code": code, "message": message})
}
func safeMessage(err error) string {
	if err == nil {
		return "invalid provider configuration"
	}
	return err.Error()
}
func writeGatewayError(c *gin.Context, err error) {
	status := http.StatusBadGateway
	code := "upstream_error"
	var pe *provider.Error
	if errors.As(err, &pe) {
		switch pe.Code {
		case provider.ErrorCodeAuthentication:
			status = http.StatusBadGateway
			code = "upstream_authentication"
		case provider.ErrorCodeRateLimit:
			status = http.StatusTooManyRequests
			code = "rate_limit"
		case provider.ErrorCodeTimeout:
			status = http.StatusGatewayTimeout
			code = "upstream_timeout"
		case provider.ErrorCodeCanceled:
			status = 499
			code = "request_canceled"
		case provider.ErrorCodeInvalidRequest:
			status = 400
			code = "invalid_request"
		case provider.ErrorCodeModelNotFound:
			status = 404
			code = "model_not_found"
		case provider.ErrorCodeUnavailable:
			status = 503
			code = "upstream_unavailable"
		}
	}
	if errors.Is(err, provider.ErrModelNotFoundInRegistry) {
		status = 404
		code = "model_not_found"
	}
	if errors.Is(err, provider.ErrProviderNotFound) {
		status = 404
		code = "provider_not_found"
	}
	if errors.Is(err, appcredential.ErrCredentialUnavailable) {
		status = http.StatusServiceUnavailable
		code = "credential_unconfigured"
	}
	if errors.Is(err, modelgateway.ErrOutputTokenLimitExceeded) || errors.Is(err, modelgateway.ErrMissingModelID) || errors.Is(err, modelgateway.ErrMissingMessages) || errors.Is(err, modelgateway.ErrInvalidMessageRole) || errors.Is(err, modelgateway.ErrEmptyMessage) || errors.Is(err, modelgateway.ErrInvalidTemperature) || errors.Is(err, modelgateway.ErrInvalidTopP) || errors.Is(err, modelgateway.ErrInvalidMaxTokens) {
		status = 400
		code = "invalid_request"
	}
	if errors.Is(err, modelgateway.ErrStreamingDisabled) {
		status = 409
		code = "streaming_unsupported"
	}
	if errors.Is(err, provider.ErrModelDisabled) || errors.Is(err, provider.ErrProviderDisabled) {
		status = 409
		code = "resource_disabled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		status = 504
		code = "request_timeout"
	}
	apiError(c, status, code, err.Error())
}
