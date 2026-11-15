package handlers

import (
	"context"
	"database/sql"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/workflow/authoring"
	"net/http"
)

type WorkflowDefinitionWriter interface {
	PutDefinition(context.Context, workflow.Definition) error
}

type WorkflowAuthoringHandler struct {
	repo   *authoring.Repository
	writer WorkflowDefinitionWriter
}

func NewWorkflowAuthoringHandler(db *sql.DB, writer WorkflowDefinitionWriter) (*WorkflowAuthoringHandler, error) {
	r, err := authoring.NewRepository(db)
	if err != nil {
		return nil, err
	}
	return &WorkflowAuthoringHandler{repo: r, writer: writer}, nil
}
func (h *WorkflowAuthoringHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/workflow-templates")
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.DELETE("/:id", h.delete)
	g.POST("/:id/apply", h.apply)
	r.GET("/workflows/:id/versions", h.versions)
}
func (h *WorkflowAuthoringHandler) create(c *gin.Context) {
	var in struct {
		ID          string              `json:"id"`
		Name        string              `json:"name"`
		Description string              `json:"description"`
		Definition  workflow.Definition `json:"definition"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	t := authoring.Template{ID: in.ID, Name: in.Name, Description: in.Description, Definition: in.Definition}
	if e := h.repo.SaveTemplate(c.Request.Context(), t); e != nil {
		c.JSON(400, gin.H{"error": e.Error()})
		return
	}
	c.JSON(201, t)
}
func (h *WorkflowAuthoringHandler) list(c *gin.Context) {
	v, e := h.repo.ListTemplates(c.Request.Context())
	if e != nil {
		c.JSON(500, gin.H{"error": "list_failed"})
		return
	}
	c.JSON(200, gin.H{"items": v})
}
func (h *WorkflowAuthoringHandler) get(c *gin.Context) {
	v, e := h.repo.GetTemplate(c.Request.Context(), c.Param("id"))
	if e != nil {
		status := 404
		if !errors.Is(e, sql.ErrNoRows) {
			status = 500
		}
		c.JSON(status, gin.H{"error": e.Error()})
		return
	}
	c.JSON(200, v)
}
func (h *WorkflowAuthoringHandler) delete(c *gin.Context) {
	if e := h.repo.DeleteTemplate(c.Request.Context(), c.Param("id")); e != nil {
		c.JSON(404, gin.H{"error": "template_not_found"})
		return
	}
	c.Status(http.StatusNoContent)
}
func (h *WorkflowAuthoringHandler) versions(c *gin.Context) {
	v, e := h.repo.Versions(c.Request.Context(), c.Param("id"))
	if e != nil {
		c.JSON(500, gin.H{"error": "versions_failed"})
		return
	}
	c.JSON(200, gin.H{"items": v})
}

func (h *WorkflowAuthoringHandler) apply(c *gin.Context) {
	if h.writer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workflow_engine_unavailable"})
		return
	}
	var in struct {
		WorkflowID string `json:"workflow_id"`
		Version    int    `json:"version"`
	}
	if c.ShouldBindJSON(&in) != nil || in.WorkflowID == "" || in.Version < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow_id and positive version are required"})
		return
	}
	t, err := h.repo.GetTemplate(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template_not_found"})
		return
	}
	d := t.Definition
	d.ID = in.WorkflowID
	d.Version = in.Version
	if err = h.writer.PutDefinition(c.Request.Context(), d); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"definition": d, "template_id": t.ID})
}
