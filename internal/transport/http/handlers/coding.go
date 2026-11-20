package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/coding"
	execdom "github.com/thkx/agentmesh/internal/domain/execution"
	execmanager "github.com/thkx/agentmesh/internal/execution/manager"
)

type CodingHandler struct {
	service *coding.Service
	manager *execmanager.Manager
}

func NewCodingHandler(s *coding.Service, managers ...*execmanager.Manager) *CodingHandler {
	h := &CodingHandler{service: s}
	if len(managers) > 0 {
		h.manager = managers[0]
	}
	return h
}

func (h *CodingHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/coding/tasks")
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.POST("/:id/run", h.run)
	g.POST("/:id/cancel", h.cancel)
	g.GET("/:id/steps", h.steps)
	g.GET("/:id/events", h.events)
	g.GET("/:id/snapshots", h.snapshots)
	g.GET("/:id/diff", h.diff)
	g.POST("/:id/patch", h.patch)
	g.POST("/:id/revert", h.revert)
	g.DELETE("/:id/workspace", h.cleanup)
}

// create submits every API-created Coding Task through the canonical
// Execution Manager. Direct Coding Service creation would bypass P4 lifecycle.
func (h *CodingHandler) create(c *gin.Context) {
	if h.manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "unified_execution_unavailable"})
		return
	}
	var in coding.CreateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request", "message": err.Error()})
		return
	}
	if err := in.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "coding_task_invalid", "message": err.Error()})
		return
	}
	input, err := json.Marshal(in)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	entity, err := h.manager.Submit(c.Request.Context(), execmanager.SubmitRequest{
		Kind: execdom.KindCoding, Runtime: execdom.RuntimeCoding, TaskID: in.ID,
		Input: input, IdempotencyKey: "api-coding:" + in.ID,
	})
	if err != nil {
		status := http.StatusBadRequest
		code := "coding_task_create_failed"
		if entity.ID != "" {
			status = http.StatusConflict
			code = "coding_execution_needs_reconciliation"
		}
		c.JSON(status, gin.H{"code": code, "execution_id": entity.ID, "message": err.Error()})
		return
	}
	task, err := h.service.Get(c.Request.Context(), in.ID)
	if err != nil {
		c.JSON(http.StatusAccepted, gin.H{"execution_id": entity.ID, "task_id": in.ID, "status": entity.Status})
		return
	}
	c.JSON(http.StatusCreated, task)
}

func (h *CodingHandler) list(c *gin.Context) {
	v, err := h.service.List(c.Request.Context(), 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "coding_task_list_failed"})
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *CodingHandler) get(c *gin.Context) {
	v, err := h.service.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		notFound(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// run asks the Execution Manager to observe the canonical execution. The
// CodingAdapter starts a persisted pending task and remains the lifecycle owner.
func (h *CodingHandler) run(c *gin.Context) {
	if h.manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "unified_execution_unavailable"})
		return
	}
	task, err := h.service.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		notFound(c, err)
		return
	}
	if task.ExecutionID == "" {
		c.JSON(http.StatusConflict, gin.H{"code": "coding_task_not_managed"})
		return
	}
	if _, err = h.manager.Get(c.Request.Context(), task.ExecutionID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"code": "coding_task_start_failed", "message": err.Error()})
		return
	}
	task, err = h.service.Get(c.Request.Context(), task.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "coding_task_read_failed"})
		return
	}
	c.JSON(http.StatusAccepted, task)
}

func (h *CodingHandler) cancel(c *gin.Context) {
	if h.manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "unified_execution_unavailable"})
		return
	}
	task, err := h.service.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		notFound(c, err)
		return
	}
	if task.ExecutionID == "" {
		c.JSON(http.StatusConflict, gin.H{"code": "coding_task_not_managed"})
		return
	}
	if err = h.manager.Cancel(c.Request.Context(), task.ExecutionID); err != nil {
		status := http.StatusConflict
		if errors.Is(err, coding.ErrNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"code": "coding_task_cancel_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "cancel_requested", "execution_id": task.ExecutionID})
}

func (h *CodingHandler) steps(c *gin.Context) {
	v, err := h.service.Steps(c.Request.Context(), c.Param("id"))
	if err != nil {
		notFound(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *CodingHandler) events(c *gin.Context) {
	v, err := h.service.Events(c.Request.Context(), c.Param("id"))
	if err != nil {
		notFound(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *CodingHandler) snapshots(c *gin.Context) {
	v, err := h.service.Snapshots(c.Request.Context(), c.Param("id"))
	if err != nil {
		notFound(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *CodingHandler) diff(c *gin.Context) {
	v, err := h.service.Diff(c.Request.Context(), c.Param("id"))
	if err != nil {
		notFound(c, err)
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(v))
}

func (h *CodingHandler) patch(c *gin.Context) {
	var in struct {
		Patch string `json:"patch"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	if err := h.service.ApplyPatch(c.Request.Context(), c.Param("id"), in.Patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "patch_rejected", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "applied"})
}

func (h *CodingHandler) revert(c *gin.Context) {
	if err := h.service.Revert(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusConflict, gin.H{"code": "revert_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "reverted"})
}

func (h *CodingHandler) cleanup(c *gin.Context) {
	if err := h.service.Cleanup(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusConflict, gin.H{"code": "cleanup_failed", "message": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func notFound(c *gin.Context, err error) {
	if errors.Is(err, coding.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"code": "coding_task_not_found"})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"code": "coding_task_error", "message": err.Error()})
}
