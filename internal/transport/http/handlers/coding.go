package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/coding"
)

type CodingHandler struct{ service *coding.Service }

func NewCodingHandler(s *coding.Service) *CodingHandler { return &CodingHandler{service: s} }
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
func (h *CodingHandler) create(c *gin.Context) {
	var in coding.CreateRequest
	if e := c.ShouldBindJSON(&in); e != nil {
		c.JSON(400, gin.H{"code": "invalid_request", "message": e.Error()})
		return
	}
	v, e := h.service.Create(c.Request.Context(), in)
	if e != nil {
		c.JSON(400, gin.H{"code": "coding_task_create_failed", "message": e.Error()})
		return
	}
	c.JSON(http.StatusCreated, v)
}
func (h *CodingHandler) list(c *gin.Context) {
	v, e := h.service.List(c.Request.Context(), 50)
	if e != nil {
		c.JSON(500, gin.H{"code": "coding_task_list_failed"})
		return
	}
	c.JSON(200, v)
}
func (h *CodingHandler) get(c *gin.Context) {
	v, e := h.service.Get(c.Request.Context(), c.Param("id"))
	if e != nil {
		notFound(c, e)
		return
	}
	c.JSON(200, v)
}
func (h *CodingHandler) run(c *gin.Context) {
	v, e := h.service.Start(c.Request.Context(), c.Param("id"))
	if e != nil {
		status := http.StatusConflict
		if errors.Is(e, coding.ErrNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"code": "coding_task_start_failed", "message": e.Error()})
		return
	}
	c.JSON(http.StatusAccepted, v)
}
func (h *CodingHandler) cancel(c *gin.Context) {
	if e := h.service.Cancel(c.Param("id")); e != nil {
		c.JSON(409, gin.H{"code": "coding_task_not_running"})
		return
	}
	c.JSON(202, gin.H{"status": "cancel_requested"})
}
func (h *CodingHandler) steps(c *gin.Context) {
	v, e := h.service.Steps(c.Request.Context(), c.Param("id"))
	if e != nil {
		notFound(c, e)
		return
	}
	c.JSON(200, v)
}
func (h *CodingHandler) events(c *gin.Context) {
	v, e := h.service.Events(c.Request.Context(), c.Param("id"))
	if e != nil {
		notFound(c, e)
		return
	}
	c.JSON(200, v)
}
func (h *CodingHandler) snapshots(c *gin.Context) {
	v, e := h.service.Snapshots(c.Request.Context(), c.Param("id"))
	if e != nil {
		notFound(c, e)
		return
	}
	c.JSON(http.StatusOK, v)
}
func (h *CodingHandler) diff(c *gin.Context) {
	v, e := h.service.Diff(c.Request.Context(), c.Param("id"))
	if e != nil {
		notFound(c, e)
		return
	}
	c.Data(200, "text/plain; charset=utf-8", []byte(v))
}
func (h *CodingHandler) patch(c *gin.Context) {
	var in struct {
		Patch string `json:"patch"`
	}
	if e := c.ShouldBindJSON(&in); e != nil {
		c.JSON(400, gin.H{"code": "invalid_request"})
		return
	}
	if e := h.service.ApplyPatch(c.Request.Context(), c.Param("id"), in.Patch); e != nil {
		c.JSON(400, gin.H{"code": "patch_rejected", "message": e.Error()})
		return
	}
	c.JSON(200, gin.H{"status": "applied"})
}
func (h *CodingHandler) revert(c *gin.Context) {
	if e := h.service.Revert(c.Request.Context(), c.Param("id")); e != nil {
		c.JSON(409, gin.H{"code": "revert_failed", "message": e.Error()})
		return
	}
	c.JSON(200, gin.H{"status": "reverted"})
}
func (h *CodingHandler) cleanup(c *gin.Context) {
	if e := h.service.Cleanup(c.Request.Context(), c.Param("id")); e != nil {
		c.JSON(409, gin.H{"code": "cleanup_failed", "message": e.Error()})
		return
	}
	c.Status(204)
}
func notFound(c *gin.Context, e error) {
	if errors.Is(e, coding.ErrNotFound) {
		c.JSON(404, gin.H{"code": "coding_task_not_found"})
		return
	}
	c.JSON(400, gin.H{"code": "coding_task_error", "message": e.Error()})
}
