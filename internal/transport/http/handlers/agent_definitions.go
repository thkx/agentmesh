package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/agentcatalog"
	dom "github.com/thkx/agentmesh/internal/domain/agent"
)

type AgentDefinitionHandler struct{ service *agentcatalog.Service }

func NewAgentDefinitionHandler(s *agentcatalog.Service) *AgentDefinitionHandler {
	return &AgentDefinitionHandler{service: s}
}
func (h *AgentDefinitionHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/agent-definitions")
	g.GET("", h.list)
	g.POST("", h.create)
	g.GET("/:id", h.get)
	g.PUT("/:id", h.update)
	g.DELETE("/:id", h.delete)
	g.POST("/:id/run", h.run)
	g.GET("/:id/runs", h.listRuns)
	r.GET("/agent-runs/:run_id", h.getRun)
}
func (h *AgentDefinitionHandler) list(c *gin.Context) {
	v, err := h.service.List(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"code": "agent_definition_list_failed"})
		return
	}
	c.JSON(200, gin.H{"data": v})
}
func (h *AgentDefinitionHandler) create(c *gin.Context) {
	var in dom.Definition
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"code": "invalid_request"})
		return
	}
	if err := h.service.Create(c.Request.Context(), in); err != nil {
		if errors.Is(err, agentcatalog.ErrExists) {
			c.JSON(409, gin.H{"code": "agent_definition_exists"})
			return
		}
		if errors.Is(err, dom.ErrInvalidDefinition) {
			c.JSON(400, gin.H{"code": "invalid_agent_definition"})
			return
		}
		c.JSON(500, gin.H{"code": "agent_definition_create_failed", "message": err.Error()})
		return
	}
	out, err := h.service.Get(c.Request.Context(), in.ID)
	if err != nil {
		c.JSON(500, gin.H{"code": "agent_definition_readback_failed"})
		return
	}
	c.JSON(http.StatusCreated, out)
}
func (h *AgentDefinitionHandler) get(c *gin.Context) {
	v, err := h.service.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"code": "agent_definition_not_found"})
		return
	}
	c.JSON(200, v)
}
func (h *AgentDefinitionHandler) update(c *gin.Context) {
	var in dom.Definition
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"code": "invalid_request"})
		return
	}
	if in.ID != "" && in.ID != c.Param("id") {
		c.JSON(400, gin.H{"code": "immutable_id"})
		return
	}
	in.ID = c.Param("id")
	if err := h.service.Update(c.Request.Context(), in); err != nil {
		if errors.Is(err, agentcatalog.ErrNotFound) {
			c.JSON(404, gin.H{"code": "agent_definition_not_found"})
			return
		}
		if errors.Is(err, dom.ErrInvalidDefinition) {
			c.JSON(400, gin.H{"code": "invalid_agent_definition"})
			return
		}
		c.JSON(409, gin.H{"code": "agent_definition_update_failed", "message": err.Error()})
		return
	}
	out, err := h.service.Get(c.Request.Context(), in.ID)
	if err != nil {
		c.JSON(500, gin.H{"code": "agent_definition_readback_failed"})
		return
	}
	c.JSON(200, out)
}
func (h *AgentDefinitionHandler) delete(c *gin.Context) {
	if err := h.service.Delete(c.Request.Context(), c.Param("id")); err != nil {
		if errors.Is(err, agentcatalog.ErrNotFound) {
			c.JSON(404, gin.H{"code": "agent_definition_not_found"})
			return
		}
		c.JSON(409, gin.H{"code": "agent_definition_delete_failed", "message": err.Error()})
		return
	}
	c.Status(204)
}
func (h *AgentDefinitionHandler) run(c *gin.Context) {
	var in struct {
		WorkspaceID string `json:"workspace_id"`
		Prompt      string `json:"prompt"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.WorkspaceID) == "" || strings.TrimSpace(in.Prompt) == "" {
		c.JSON(400, gin.H{"code": "invalid_request"})
		return
	}
	result, err := h.service.Run(c.Request.Context(), c.Param("id"), in.WorkspaceID, in.Prompt)
	if err != nil {
		status := 400
		if errors.Is(err, context.DeadlineExceeded) {
			status = 504
		}
		c.JSON(status, gin.H{"code": "agent_run_failed", "message": err.Error(), "result": result})
		return
	}
	c.JSON(200, result)
}

func (h *AgentDefinitionHandler) listRuns(c *gin.Context) {
	v, err := h.service.ListRuns(c.Request.Context(), c.Param("id"), 100)
	if err != nil {
		if errors.Is(err, agentcatalog.ErrNotFound) {
			c.JSON(404, gin.H{"code": "agent_definition_not_found"})
			return
		}
		c.JSON(500, gin.H{"code": "agent_runs_list_failed"})
		return
	}
	c.JSON(200, gin.H{"data": v})
}
func (h *AgentDefinitionHandler) getRun(c *gin.Context) {
	v, err := h.service.GetRun(c.Request.Context(), c.Param("run_id"))
	if err != nil {
		c.JSON(404, gin.H{"code": "agent_run_not_found"})
		return
	}
	c.JSON(200, v)
}
