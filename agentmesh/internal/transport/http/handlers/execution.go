package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/agent/executor"
	"github.com/thkx/agentmesh/internal/domain/execution"
)

type ExecutionHandler struct{ executor executor.AgentExecutor }

func NewExecutionHandler(e executor.AgentExecutor) *ExecutionHandler {
	return &ExecutionHandler{executor: e}
}
func (h *ExecutionHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/executions")
	g.POST("", h.execute)
	g.DELETE("/:id", h.cancel)
}
func (h *ExecutionHandler) execute(c *gin.Context) {
	var in struct {
		ID             string                   `json:"id"`
		Provider       execution.Provider       `json:"provider"`
		Prompt         string                   `json:"prompt"`
		WorkspacePath  string                   `json:"workspace_path"`
		Model          string                   `json:"model"`
		PermissionMode execution.PermissionMode `json:"permission_mode"`
		TimeoutSeconds int                      `json:"timeout_seconds"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"code": "invalid_request", "message": "Invalid execution request"})
		return
	}
	if in.TimeoutSeconds < 0 || in.TimeoutSeconds > 24*60*60 {
		c.JSON(400, gin.H{"code": "invalid_timeout", "message": "timeout_seconds must be between 0 and 86400"})
		return
	}
	req := execution.Request{ID: strings.TrimSpace(in.ID), Provider: in.Provider, Prompt: in.Prompt, WorkspacePath: in.WorkspacePath, Model: in.Model, PermissionMode: in.PermissionMode, Timeout: time.Duration(in.TimeoutSeconds) * time.Second}
	if err := req.Validate(); err != nil {
		c.JSON(400, gin.H{"code": "invalid_execution", "message": err.Error()})
		return
	}
	result, err := h.executor.Execute(c.Request.Context(), req)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.Canceled) {
			status = 499
		}
		if errors.Is(err, context.DeadlineExceeded) {
			status = 504
		}
		c.JSON(status, gin.H{"error": err.Error(), "result": result})
		return
	}
	c.JSON(http.StatusOK, result)
}
func (h *ExecutionHandler) cancel(c *gin.Context) {
	if err := h.executor.Cancel(c.Request.Context(), c.Param("id")); err != nil {
		if errors.Is(err, execution.ErrExecutionNotFound) {
			c.JSON(404, gin.H{"code": "execution_not_found", "message": "Execution is not running"})
			return
		}
		c.JSON(500, gin.H{"code": "cancel_failed", "message": "Could not cancel execution"})
		return
	}
	c.Status(http.StatusAccepted)
}
