package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/collaboration"
)

// ArtifactProvenanceHandler exposes lineage while keeping Artifact ownership
// and content lifecycle in the existing Collaboration service.
type ArtifactProvenanceHandler struct {
	service *collaboration.Service
}

func NewArtifactProvenanceHandler(service *collaboration.Service) *ArtifactProvenanceHandler {
	return &ArtifactProvenanceHandler{service: service}
}

func (h *ArtifactProvenanceHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/collaboration")
	g.GET("/artifacts/:id/provenance", h.get)
	g.POST("/artifacts/:id/versions/:version/provenance", h.bindVersionAttempt)
	g.POST("/artifacts/:id/evidence/provenance", h.createEvidence)
}

func (h *ArtifactProvenanceHandler) get(c *gin.Context) {
	result, err := h.service.GetArtifactProvenance(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *ArtifactProvenanceHandler) bindVersionAttempt(c *gin.Context) {
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_version"})
		return
	}
	var in struct {
		ExecutionID string `json:"execution_id"`
		AttemptID   string `json:"attempt_id"`
	}
	if err = c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	err = h.service.BindArtifactVersionAttempt(c.Request.Context(), c.Param("id"), version,
		collaboration.BindArtifactVersionAttemptRequest{ExecutionID: in.ExecutionID, AttemptID: in.AttemptID})
	if err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *ArtifactProvenanceHandler) createEvidence(c *gin.Context) {
	var in struct {
		VersionID     string `json:"version_id"`
		Kind          string `json:"kind"`
		SourceRef     string `json:"source_ref"`
		ContentSHA256 string `json:"content_sha256"`
		Statement     string `json:"statement"`
		CreatedBy     string `json:"created_by"`
		ExecutionID   string `json:"execution_id"`
		AttemptID     string `json:"attempt_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	result, err := h.service.CreateArtifactEvidenceWithProvenance(c.Request.Context(), c.Param("id"),
		collaboration.CreateArtifactEvidenceProvenanceRequest{
			VersionID: in.VersionID, Kind: in.Kind, SourceRef: in.SourceRef,
			ContentSHA256: in.ContentSHA256, Statement: in.Statement, CreatedBy: in.CreatedBy,
			ExecutionID: in.ExecutionID, AttemptID: in.AttemptID,
		})
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (h *ArtifactProvenanceHandler) fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, collaboration.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": "artifact_provenance_not_found"})
	case errors.Is(err, collaboration.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"code": "artifact_provenance_conflict"})
	case errors.Is(err, collaboration.ErrInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_artifact_provenance"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"code": "artifact_provenance_failed"})
	}
}
