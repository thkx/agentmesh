package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	agentruntime "github.com/thkx/agentmesh/internal/agent/runtime"
	"github.com/thkx/agentmesh/internal/agent/scheduler"
	dom "github.com/thkx/agentmesh/internal/domain/agent"
)

type AgentHandler struct {
	runtime     *agentruntime.Runtime
	scheduler   scheduler.Store
	maxAttempts int
}

func NewAgentHandler(r *agentruntime.Runtime, stores ...scheduler.Store) *AgentHandler {
	h := &AgentHandler{runtime: r, maxAttempts: 3}
	if len(stores) > 0 {
		h.scheduler = stores[0]
	}
	return h
}
func (h *AgentHandler) SetMaxAttempts(n int) {
	if n > 0 {
		h.maxAttempts = n
	}
}

func (h *AgentHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/agents")
	g.POST("/run", h.run)
	if h.scheduler != nil {
		g.POST("/queue", h.enqueue)
		g.GET("/jobs/:id", h.getJob)
		g.POST("/jobs/:id/retry", h.retryJob)
	}
	g.GET("/tasks/:id", h.get)
	g.GET("/tasks/:id/messages", h.messages)
	g.POST("/tasks/:id/cancel", h.cancel)
}
func (h *AgentHandler) run(c *gin.Context) {
	var in dom.Task
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	result, err := h.runtime.Run(c.Request.Context(), in)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		} else if errors.Is(err, context.Canceled) {
			status = 499
		}
		c.JSON(status, gin.H{"code": "agent_execution_failed", "message": err.Error(), "result": result})
		return
	}
	c.JSON(http.StatusOK, result)
}
func (h *AgentHandler) get(c *gin.Context) {
	v, err := h.runtime.Get(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "task_not_found"})
		return
	}
	c.JSON(http.StatusOK, v)
}
func (h *AgentHandler) cancel(c *gin.Context) {
	if err := h.runtime.Cancel(c.Param("id")); err != nil {
		if h.scheduler != nil && errors.Is(err, dom.ErrTaskNotFound) {
			if cancelErr := h.scheduler.CancelTask(c.Request.Context(), c.Param("id")); cancelErr == nil {
				c.JSON(http.StatusAccepted, gin.H{"status": "canceled"})
				return
			}
		}
		c.JSON(http.StatusNotFound, gin.H{"code": "task_not_found_or_already_started"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "cancel_requested"})
}

func (h *AgentHandler) messages(c *gin.Context) {
	snapshot, err := h.runtime.GetSnapshot(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "task_not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": snapshot.Task.ID, "messages": snapshot.Messages, "updated_at": snapshot.UpdatedAt})
}

func (h *AgentHandler) enqueue(c *gin.Context) {
	var task dom.Task
	if err := c.ShouldBindJSON(&task); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	maxAttempts := h.maxAttempts
	jobID := c.GetHeader("Idempotency-Key")
	if jobID == "" {
		jobID = task.ID
	}
	job, err := h.scheduler.Enqueue(c.Request.Context(), task, jobID, maxAttempts)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"code": "job_enqueue_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, job)
}
func (h *AgentHandler) getJob(c *gin.Context) {
	job, err := h.scheduler.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "job_not_found"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *AgentHandler) retryJob(c *gin.Context) {
	store, ok := h.scheduler.(interface {
		Requeue(context.Context, string) (scheduler.Job, error)
	})
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"code": "retry_not_supported"})
		return
	}
	job, err := store.Requeue(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"code": "job_not_retriable", "message": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, job)
}
