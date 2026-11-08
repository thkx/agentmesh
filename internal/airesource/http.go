package airesource

import (
	"crypto/subtle"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"strings"
)

type HTTPHandler struct {
	service  *Service
	router   *Router
	apiToken string
}

func NewHTTPHandler(s *Service, r *Router, apiToken string) (*HTTPHandler, error) {
	if s == nil || r == nil {
		return nil, ErrInvalid
	}
	return &HTTPHandler{service: s, router: r, apiToken: apiToken}, nil
}
func (h *HTTPHandler) RegisterRoutes(engine *gin.Engine) {
	api := engine.Group("/api/v1/resources")
	api.Use(h.localOrToken())
	api.GET("/providers", h.listProviders)
	api.POST("/providers", h.createProvider)
	api.PUT("/providers/:id", h.updateProvider)
	api.DELETE("/providers/:id", h.deleteProvider)
	api.GET("/endpoints", h.listEndpoints)
	api.POST("/endpoints", h.createEndpoint)
	api.PUT("/endpoints/:id", h.updateEndpoint)
	api.DELETE("/endpoints/:id", h.deleteEndpoint)
	api.GET("/credentials", h.listCredentials)
	api.POST("/credentials", h.createCredential)
	api.PUT("/credentials/:id", h.rotateCredential)
	api.POST("/credentials/bulk", h.createCredentialsBulk)
	api.PATCH("/credentials/:id/status", h.setCredentialStatus)
	api.DELETE("/credentials/:id", h.deleteCredential)
	api.POST("/credentials/:id/test", h.testCredential)
	api.GET("/credentials/:id/models", h.discoverModels)
	api.POST("/health/check/:id", h.checkCredential)
	api.GET("/models", h.listModels)
	api.POST("/models", h.createModel)
	api.PUT("/models/:id", h.updateModel)
	api.DELETE("/models/:id", h.deleteModel)
	api.GET("/bindings", h.listBindings)
	api.PUT("/bindings", h.bindModel)
	api.GET("/policies", h.listPolicies)
	api.PUT("/policies/:model_id", h.setPolicy)
	api.GET("/health", h.health)
	api.GET("/usage", h.usage)
	api.GET("/requests", h.requests)
	gateway := engine.Group("/v1")
	gateway.Use(h.localOrToken())
	gateway.POST("/chat/completions", h.chatCompletions)
	gateway.POST("/responses", h.responses)
}
func (h *HTTPHandler) localOrToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.apiToken != "" {
			parts := strings.Fields(c.GetHeader("Authorization"))
			got := ""
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				got = parts[1]
			}
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(h.apiToken)) != 1 {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"type": "authentication_error", "message": "valid Bearer API token required"}})
				return
			}
		}
		c.Next()
	}
}
func bad(c *gin.Context, code int, msg string) { c.JSON(code, gin.H{"error": gin.H{"message": msg}}) }
func (h *HTTPHandler) listProviders(c *gin.Context) {
	v, e := h.service.ListProviders(c.Request.Context())
	if e != nil {
		bad(c, 500, e.Error())
		return
	}
	c.JSON(200, gin.H{"data": v})
}
func (h *HTTPHandler) createProvider(c *gin.Context) {
	var v Provider
	if c.ShouldBindJSON(&v) != nil {
		bad(c, 400, "invalid JSON")
		return
	}
	if e := h.service.CreateProvider(c.Request.Context(), v); e != nil {
		bad(c, 400, e.Error())
		return
	}
	c.JSON(201, v)
}
func (h *HTTPHandler) updateProvider(c *gin.Context) {
	var v Provider
	if c.ShouldBindJSON(&v) != nil {
		bad(c, 400, "invalid JSON")
		return
	}
	v.ID = c.Param("id")
	if err := h.service.UpdateProvider(c.Request.Context(), v); err != nil {
		bad(c, 400, err.Error())
		return
	}
	c.JSON(200, v)
}
func (h *HTTPHandler) deleteProvider(c *gin.Context) {
	if err := h.service.DeleteProvider(c.Request.Context(), c.Param("id")); err != nil {
		bad(c, 409, err.Error())
		return
	}
	c.Status(204)
}

func (h *HTTPHandler) listEndpoints(c *gin.Context) {
	v, e := h.service.ListEndpoints(c.Request.Context())
	if e != nil {
		bad(c, 500, e.Error())
		return
	}
	c.JSON(200, gin.H{"data": v})
}
func (h *HTTPHandler) createEndpoint(c *gin.Context) {
	var v Endpoint
	if c.ShouldBindJSON(&v) != nil {
		bad(c, 400, "invalid JSON")
		return
	}
	if e := h.service.CreateEndpoint(c.Request.Context(), v); e != nil {
		bad(c, 400, e.Error())
		return
	}
	c.JSON(201, v)
}
func (h *HTTPHandler) updateEndpoint(c *gin.Context) {
	var v Endpoint
	if c.ShouldBindJSON(&v) != nil {
		bad(c, 400, "invalid JSON")
		return
	}
	v.ID = c.Param("id")
	if err := h.service.UpdateEndpoint(c.Request.Context(), v); err != nil {
		bad(c, 400, err.Error())
		return
	}
	c.JSON(200, v)
}
func (h *HTTPHandler) deleteEndpoint(c *gin.Context) {
	if err := h.service.DeleteEndpoint(c.Request.Context(), c.Param("id")); err != nil {
		bad(c, 409, err.Error())
		return
	}
	c.Status(204)
}

func (h *HTTPHandler) listCredentials(c *gin.Context) {
	v, e := h.service.ListCredentials(c.Request.Context())
	if e != nil {
		bad(c, 500, e.Error())
		return
	}
	c.JSON(200, gin.H{"data": v})
}
func (h *HTTPHandler) createCredential(c *gin.Context) {
	var v CredentialInput
	if c.ShouldBindJSON(&v) != nil {
		bad(c, 400, "invalid JSON")
		return
	}
	if e := h.service.CreateCredential(c.Request.Context(), v); e != nil {
		bad(c, 400, e.Error())
		return
	}
	v.APIKey = ""
	c.JSON(201, gin.H{"id": v.ID, "name": v.Name, "status": "active"})
}
func (h *HTTPHandler) rotateCredential(c *gin.Context) {
	var v struct {
		Name           string `json:"name"`
		APIKey         string `json:"api_key"`
		Priority       int    `json:"priority"`
		Weight         int    `json:"weight"`
		MaxConcurrency int    `json:"max_concurrency"`
	}
	if c.ShouldBindJSON(&v) != nil {
		bad(c, 400, "invalid JSON")
		return
	}
	if err := h.service.RotateCredential(c.Request.Context(), c.Param("id"), v.Name, v.APIKey, v.Priority, v.Weight, v.MaxConcurrency); err != nil {
		bad(c, 400, err.Error())
		return
	}
	c.JSON(200, gin.H{"id": c.Param("id"), "status": "active", "rotated": true})
}

func (h *HTTPHandler) createCredentialsBulk(c *gin.Context) {
	var in struct {
		Items []CredentialInput `json:"items"`
	}
	if c.ShouldBindJSON(&in) != nil {
		bad(c, 400, "invalid JSON")
		return
	}
	if err := h.service.CreateCredentialsBulk(c.Request.Context(), in.Items); err != nil {
		bad(c, 400, err.Error())
		return
	}
	ids := make([]string, 0, len(in.Items))
	for _, item := range in.Items {
		ids = append(ids, item.ID)
	}
	c.JSON(http.StatusCreated, gin.H{"created": len(ids), "ids": ids})
}

func (h *HTTPHandler) setCredentialStatus(c *gin.Context) {
	var v struct {
		Status string `json:"status"`
	}
	if c.ShouldBindJSON(&v) != nil {
		bad(c, 400, "invalid JSON")
		return
	}
	if e := h.service.SetCredentialStatus(c.Request.Context(), c.Param("id"), v.Status); e != nil {
		bad(c, 400, e.Error())
		return
	}
	c.JSON(200, gin.H{"id": c.Param("id"), "status": v.Status})
}
func (h *HTTPHandler) deleteCredential(c *gin.Context) {
	if e := h.service.DeleteCredential(c.Request.Context(), c.Param("id")); e != nil {
		bad(c, 404, e.Error())
		return
	}
	c.Status(204)
}
func (h *HTTPHandler) testCredential(c *gin.Context) {
	if e := h.service.TestCredential(c.Request.Context(), c.Param("id")); e != nil {
		bad(c, 502, e.Error())
		return
	}
	c.JSON(200, gin.H{"id": c.Param("id"), "status": "reachable"})
}
func (h *HTTPHandler) discoverModels(c *gin.Context) {
	items, err := h.service.DiscoverModels(c.Request.Context(), c.Param("id"))
	if err != nil {
		bad(c, 502, err.Error())
		return
	}
	c.JSON(200, gin.H{"data": items})
}
func (h *HTTPHandler) checkCredential(c *gin.Context) {
	if err := h.service.CheckCredential(c.Request.Context(), c.Param("id")); err != nil {
		bad(c, 502, err.Error())
		return
	}
	c.JSON(200, gin.H{"id": c.Param("id"), "state": "healthy"})
}

func (h *HTTPHandler) listModels(c *gin.Context) {
	v, e := h.service.ListModels(c.Request.Context())
	if e != nil {
		bad(c, 500, e.Error())
		return
	}
	c.JSON(200, gin.H{"data": v})
}
func (h *HTTPHandler) createModel(c *gin.Context) {
	var v Model
	if c.ShouldBindJSON(&v) != nil {
		bad(c, 400, "invalid JSON")
		return
	}
	if e := h.service.CreateModel(c.Request.Context(), v); e != nil {
		bad(c, 400, e.Error())
		return
	}
	c.JSON(201, v)
}
func (h *HTTPHandler) updateModel(c *gin.Context) {
	var v Model
	if c.ShouldBindJSON(&v) != nil {
		bad(c, 400, "invalid JSON")
		return
	}
	v.ID = c.Param("id")
	if err := h.service.UpdateModel(c.Request.Context(), v); err != nil {
		bad(c, 400, err.Error())
		return
	}
	c.JSON(200, v)
}
func (h *HTTPHandler) deleteModel(c *gin.Context) {
	if err := h.service.DeleteModel(c.Request.Context(), c.Param("id")); err != nil {
		bad(c, 409, err.Error())
		return
	}
	c.Status(204)
}

func (h *HTTPHandler) listBindings(c *gin.Context) {
	v, e := h.service.ListBindings(c.Request.Context())
	if e != nil {
		bad(c, 500, e.Error())
		return
	}
	c.JSON(200, gin.H{"data": v})
}
func (h *HTTPHandler) bindModel(c *gin.Context) {
	var v BindingInput
	if c.ShouldBindJSON(&v) != nil {
		bad(c, 400, "invalid JSON")
		return
	}
	if e := h.service.BindModel(c.Request.Context(), v); e != nil {
		bad(c, 400, e.Error())
		return
	}
	c.JSON(200, gin.H{"data": v})
}
func (h *HTTPHandler) listPolicies(c *gin.Context) {
	v, e := h.service.ListPolicies(c.Request.Context())
	if e != nil {
		bad(c, 500, e.Error())
		return
	}
	c.JSON(200, gin.H{"data": v})
}
func (h *HTTPHandler) setPolicy(c *gin.Context) {
	var v RoutingPolicy
	if c.ShouldBindJSON(&v) != nil {
		bad(c, 400, "invalid JSON")
		return
	}
	v.ModelID = c.Param("model_id")
	if e := h.service.SetPolicy(c.Request.Context(), v); e != nil {
		bad(c, 400, e.Error())
		return
	}
	c.JSON(200, v)
}
func (h *HTTPHandler) health(c *gin.Context) {
	v, e := h.service.Health(c.Request.Context())
	if e != nil {
		bad(c, 500, e.Error())
		return
	}
	c.JSON(200, gin.H{"data": v})
}
func (h *HTTPHandler) usage(c *gin.Context) {
	v, e := h.service.Usage(c.Request.Context())
	if e != nil {
		bad(c, 500, e.Error())
		return
	}
	c.JSON(200, v)
}
func (h *HTTPHandler) requests(c *gin.Context) {
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	items, err := h.service.ListRequests(c.Request.Context(), limit)
	if err != nil {
		bad(c, 500, err.Error())
		return
	}
	c.JSON(200, gin.H{"data": items})
}

func (h *HTTPHandler) chatCompletions(c *gin.Context) {
	var req GenerateRequest
	if c.ShouldBindJSON(&req) != nil {
		bad(c, 400, "invalid chat completion request")
		return
	}
	resp, e := h.router.Generate(c.Request.Context(), req)
	if e != nil {
		bad(c, 502, e.Error())
		return
	}
	c.JSON(200, resp)
}
func (h *HTTPHandler) responses(c *gin.Context) {
	var in struct {
		Model           string   `json:"model"`
		Input           string   `json:"input"`
		MaxOutputTokens int      `json:"max_output_tokens"`
		Temperature     *float64 `json:"temperature"`
		Stream          bool     `json:"stream"`
	}
	if c.ShouldBindJSON(&in) != nil {
		bad(c, 400, "invalid responses request")
		return
	}
	req := GenerateRequest{Model: in.Model, Messages: []Message{{Role: "user", Content: in.Input}}, MaxTokens: in.MaxOutputTokens, Temperature: in.Temperature, Stream: in.Stream}
	resp, e := h.router.Generate(c.Request.Context(), req)
	if e != nil {
		bad(c, 502, e.Error())
		return
	}
	output := ""
	if len(resp.Choices) > 0 {
		output = resp.Choices[0].Message.Content
	}
	c.JSON(200, gin.H{"id": resp.ID, "object": "response", "status": "completed", "model": resp.Model, "output": []any{gin.H{"type": "message", "role": "assistant", "content": []any{gin.H{"type": "output_text", "text": output}}}}, "usage": gin.H{"input_tokens": resp.Usage.PromptTokens, "output_tokens": resp.Usage.CompletionTokens, "total_tokens": resp.Usage.TotalTokens}})
}
