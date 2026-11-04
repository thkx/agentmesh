package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	dom "github.com/thkx/agentmesh/internal/domain/memory"
	reflectiondom "github.com/thkx/agentmesh/internal/domain/reflection"
	"github.com/thkx/agentmesh/internal/memory"
)

type MemoryService interface {
	IngestReflection(context.Context, string, memory.IngestRequest) ([]dom.Memory, error)
	ListByGoal(context.Context, string, int) ([]dom.Memory, error)
	Promote(context.Context, string, memory.PromoteRequest) (dom.Memory, error)
	Archive(context.Context, string) (dom.Memory, error)
	Reject(context.Context, string, memory.RejectRequest) (dom.Memory, error)
	Search(context.Context, memory.SearchRequest) (memory.SearchResult, error)
}

type MemoryHandler struct{ service MemoryService }

func NewMemoryHandler(service MemoryService) *MemoryHandler { return &MemoryHandler{service: service} }
func (h *MemoryHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/goals/:id/memories/ingest", h.ingest)
	r.GET("/goals/:id/memories", h.list)
	r.POST("/memories/search", h.search)
	r.POST("/memories/:memory_id/promote", h.promote)
	r.POST("/memories/:memory_id/archive", h.archive)
	r.POST("/memories/:memory_id/reject", h.reject)
}
func (h *MemoryHandler) ingest(c *gin.Context) {
	var req memory.IngestRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.RunID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request", "message": "run_id is required"})
		return
	}
	items, err := h.service.IngestReflection(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, dom.ErrNotFound) || errors.Is(err, reflectiondom.ErrNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"code": "memory_ingest_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"items": items, "count": len(items)})
}
func (h *MemoryHandler) list(c *gin.Context) {
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_limit"})
			return
		}
		limit = n
	}
	items, err := h.service.ListByGoal(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "memory_list_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
func (h *MemoryHandler) search(c *gin.Context) {
	var req memory.SearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	result, err := h.service.Search(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidRequest) {
			c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_search_request"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": "memory_search_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}
func (h *MemoryHandler) promote(c *gin.Context) {
	var req memory.PromoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	item, err := h.service.Promote(c.Request.Context(), c.Param("memory_id"), req)
	if err != nil {
		h.writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}
func (h *MemoryHandler) archive(c *gin.Context) {
	item, err := h.service.Archive(c.Request.Context(), c.Param("memory_id"))
	if err != nil {
		h.writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}
func (h *MemoryHandler) reject(c *gin.Context) {
	var req memory.RejectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	item, err := h.service.Reject(c.Request.Context(), c.Param("memory_id"), req)
	if err != nil {
		h.writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}
func (h *MemoryHandler) writeMutationError(c *gin.Context, err error) {
	if errors.Is(err, dom.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"code": "memory_not_found"})
		return
	}
	if errors.Is(err, dom.ErrInvalidState) {
		c.JSON(http.StatusConflict, gin.H{"code": "invalid_memory_transition", "message": err.Error()})
		return
	}
	if errors.Is(err, memory.ErrInvalidRequest) || errors.Is(err, dom.ErrInvalidMemory) {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_memory_request", "message": err.Error()})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"code": "memory_mutation_failed", "message": err.Error()})
}
