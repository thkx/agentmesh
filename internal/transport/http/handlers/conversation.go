package handlers

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/conversation"
	"net/http"
	"strconv"
)

type ConversationHandler struct{ service *conversation.Service }

func NewConversationHandler(s *conversation.Service) *ConversationHandler {
	return &ConversationHandler{service: s}
}
func (h *ConversationHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/conversations")
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.POST("/:id/messages", h.append)
	g.GET("/:id/messages", h.messages)
	g.POST("/:id/archive", h.archive)
}
func (h *ConversationHandler) create(c *gin.Context) {
	var in struct {
		Title     string             `json:"title"`
		ScopeType conversation.Scope `json:"scope_type"`
		ScopeID   string             `json:"scope_id"`
		Metadata  map[string]any     `json:"metadata"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	v, e := h.service.Create(c.Request.Context(), in.Title, in.ScopeType, in.ScopeID, in.Metadata)
	if e != nil {
		c.JSON(400, gin.H{"error": e.Error()})
		return
	}
	c.JSON(201, v)
}
func (h *ConversationHandler) list(c *gin.Context) {
	v, e := h.service.List(c.Request.Context(), conversation.Scope(c.Query("scope_type")), c.Query("scope_id"), 50)
	if e != nil {
		c.JSON(500, gin.H{"error": "list_failed"})
		return
	}
	c.JSON(200, gin.H{"items": v})
}
func (h *ConversationHandler) get(c *gin.Context) {
	v, e := h.service.Repository().Get(c.Request.Context(), c.Param("id"))
	if e != nil {
		status := 404
		if !errors.Is(e, conversation.ErrNotFound) {
			status = 500
		}
		c.JSON(status, gin.H{"error": e.Error()})
		return
	}
	c.JSON(200, v)
}
func (h *ConversationHandler) append(c *gin.Context) {
	var in struct {
		Role          string `json:"role"`
		Name          string `json:"name"`
		Content       string `json:"content"`
		ToolCallID    string `json:"tool_call_id"`
		ToolCallsJSON string `json:"tool_calls_json"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	v, e := h.service.Append(c.Request.Context(), c.Param("id"), in.Role, in.Name, in.Content, in.ToolCallID, in.ToolCallsJSON)
	if e != nil {
		c.JSON(400, gin.H{"error": e.Error()})
		return
	}
	c.JSON(201, v)
}
func (h *ConversationHandler) messages(c *gin.Context) {
	after, _ := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	v, e := h.service.Messages(c.Request.Context(), c.Param("id"), after, limit)
	if e != nil {
		c.JSON(404, gin.H{"error": e.Error()})
		return
	}
	c.JSON(200, gin.H{"items": v})
}
func (h *ConversationHandler) archive(c *gin.Context) {
	if e := h.service.Archive(c.Request.Context(), c.Param("id")); e != nil {
		c.JSON(404, gin.H{"error": e.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
