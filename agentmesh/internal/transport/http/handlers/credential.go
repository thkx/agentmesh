package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	appcredential "github.com/thkx/agentmesh/internal/credential"
	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
)

type CredentialHandler struct{ manager appcredential.Manager }

func NewCredentialHandler(manager appcredential.Manager) *CredentialHandler {
	return &CredentialHandler{manager: manager}
}
func (h *CredentialHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/providers/:id/credential")
	g.GET("", h.get)
	g.PUT("", h.put)
	g.DELETE("", h.delete)
}
func (h *CredentialHandler) get(c *gin.Context) {
	record, err := h.manager.Status(c.Request.Context(), provider.ID(c.Param("id")))
	if err != nil {
		credentialError(c, err)
		return
	}
	c.JSON(http.StatusOK, record)
}
func (h *CredentialHandler) put(c *gin.Context) {
	var in struct {
		APIKey string `json:"api_key"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"code": "invalid_json", "message": "Request must contain a valid api_key"})
		return
	}
	if strings.TrimSpace(in.APIKey) == "" {
		c.JSON(400, gin.H{"code": "empty_credential", "message": "api_key must not be empty"})
		return
	}
	secret, err := domaincredential.NewSecret(in.APIKey)
	if err != nil {
		credentialError(c, err)
		return
	}
	if err := h.manager.Set(c.Request.Context(), provider.ID(c.Param("id")), secret); err != nil {
		credentialError(c, err)
		return
	}
	record, err := h.manager.Status(context.WithoutCancel(c.Request.Context()), provider.ID(c.Param("id")))
	if err != nil {
		credentialError(c, err)
		return
	}
	c.JSON(http.StatusOK, record)
}
func (h *CredentialHandler) delete(c *gin.Context) {
	if err := h.manager.Delete(c.Request.Context(), provider.ID(c.Param("id"))); err != nil {
		credentialError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func credentialError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, appcredential.ErrProviderNotFound):
		c.JSON(404, gin.H{"code": "provider_not_found", "message": "Provider does not exist"})
	case errors.Is(err, domaincredential.ErrNotFound):
		c.JSON(404, gin.H{"code": "credential_not_found", "message": "Stored credential does not exist"})
	case errors.Is(err, domaincredential.ErrEmptySecret):
		c.JSON(400, gin.H{"code": "empty_credential", "message": "Credential must not be empty"})
	case errors.Is(err, context.Canceled):
		c.JSON(499, gin.H{"code": "request_canceled", "message": "Request canceled"})
	default:
		c.JSON(500, gin.H{"code": "credential_operation_failed", "message": "Credential operation failed"})
	}
}
