package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/workspace"
)

type WorkspaceHandler struct{ manager *workspace.Manager }

func NewWorkspaceHandler(m *workspace.Manager) *WorkspaceHandler {
	return &WorkspaceHandler{manager: m}
}
func (h *WorkspaceHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/workspaces", h.create)
	r.DELETE("/workspaces/:id", h.remove)
}
func (h *WorkspaceHandler) create(c *gin.Context) {
	var in struct {
		ID string `json:"id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"code": "invalid_request", "message": "id is required"})
		return
	}
	w, err := h.manager.Create(c.Request.Context(), in.ID)
	if err != nil {
		c.JSON(400, gin.H{"code": "workspace_create_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, w)
}
func (h *WorkspaceHandler) remove(c *gin.Context) {
	if err := h.manager.Remove(c.Param("id")); err != nil {
		if errors.Is(err, workspace.ErrWorkspaceInUse) {
			c.JSON(http.StatusConflict, gin.H{"code": "workspace_in_use", "message": "Workspace has an active execution"})
			return
		}
		c.JSON(404, gin.H{"code": "workspace_remove_failed", "message": "Workspace could not be removed"})
		return
	}
	c.Status(http.StatusNoContent)
}
