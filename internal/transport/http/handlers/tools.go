package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	dom "github.com/thkx/agentmesh/internal/domain/tool"
	"github.com/thkx/agentmesh/internal/tools"
	"github.com/thkx/agentmesh/internal/tools/audit"
	"github.com/thkx/agentmesh/internal/workspace"
)

type ToolHandler struct {
	engine     *tools.Engine
	registry   *tools.Registry
	workspaces *workspace.Manager
	auditor    *audit.Memory
}

func NewToolHandler(e *tools.Engine, r *tools.Registry, w *workspace.Manager, a *audit.Memory) *ToolHandler {
	return &ToolHandler{engine: e, registry: r, workspaces: w, auditor: a}
}
func (h *ToolHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/tools")
	g.GET("", h.list)
	g.POST("/:name/invoke", h.invoke)
	g.GET("/audit", h.listAudit)
}
func (h *ToolHandler) list(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"tools": h.registry.List()}) }
func (h *ToolHandler) invoke(c *gin.Context) {
	var in struct {
		WorkspaceID string          `json:"workspace_id"`
		Arguments   json.RawMessage `json:"arguments"`
		Approved    bool            `json:"approved"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"code": "invalid_request"})
		return
	}
	ws, err := h.workspaces.Resolve(in.WorkspaceID)
	if err != nil {
		c.JSON(400, gin.H{"code": "invalid_workspace"})
		return
	}
	release, err := h.workspaces.AcquirePath(ws.Path)
	if err != nil {
		c.JSON(409, gin.H{"code": "workspace_unavailable"})
		return
	}
	defer release()
	result, err := h.engine.Invoke(c.Request.Context(), c.Param("name"), in.Arguments, ws.Path, in.Approved)
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, dom.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, dom.ErrDenied):
			status = http.StatusForbidden
		case errors.Is(err, dom.ErrApprovalRequired):
			status = http.StatusConflict
		case errors.Is(err, context.Canceled):
			status = 499
		}
		c.JSON(status, gin.H{"code": "tool_invocation_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": result})
}
func (h *ToolHandler) listAudit(c *gin.Context) {
	limit := 100
	if n := c.Query("limit"); n != "" {
		var v int
		if _, err := fmt.Sscanf(n, "%d", &v); err == nil && v > 0 && v < 1001 {
			limit = v
		}
	}
	c.JSON(http.StatusOK, gin.H{"events": h.auditor.List(limit)})
}
