package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	dom "github.com/thkx/agentmesh/internal/domain/reflection"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/reflection"
)

type ReflectionService interface {
	Create(context.Context, string, reflection.CreateRequest) (dom.Reflection, error)
	Get(context.Context, string, string) (dom.Reflection, error)
	List(context.Context, string, int) ([]dom.Reflection, error)
}
type ReflectionHandler struct{ service ReflectionService }

func NewReflectionHandler(s ReflectionService) *ReflectionHandler {
	return &ReflectionHandler{service: s}
}
func (h *ReflectionHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/goals/:id/reflections")
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:run_id", h.get)
}
func (h *ReflectionHandler) create(c *gin.Context) {
	var req reflection.CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.RunID == "" || req.ModelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request", "message": "run_id and model_id are required"})
		return
	}
	item, err := h.service.Create(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, dom.ErrNotFound) || errors.Is(err, goaldom.ErrNotFound) || errors.Is(err, wf.ErrNotFound) {
			status = http.StatusNotFound
		}
		if errors.Is(err, reflection.ErrEvaluationRequired) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"code": "reflection_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, item)
}
func (h *ReflectionHandler) list(c *gin.Context) {
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_limit"})
			return
		}
		limit = n
	}
	items, err := h.service.List(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "reflection_list_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
func (h *ReflectionHandler) get(c *gin.Context) {
	item, err := h.service.Get(c.Request.Context(), c.Param("id"), c.Param("run_id"))
	if err != nil {
		if errors.Is(err, dom.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"code": "reflection_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": "reflection_read_failed"})
		return
	}
	c.JSON(http.StatusOK, item)
}
