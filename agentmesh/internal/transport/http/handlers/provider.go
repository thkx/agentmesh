package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/transport/http/dto"
)

var (
	ErrProviderNotFound = errors.New("provider not found")
	ErrProviderDisabled = errors.New("provider is disabled")
)

type ProviderInfo struct {
	ID      provider.ID
	Name    string
	Type    string
	Status  string
	BaseURL string
}

type ProviderRegistry interface {
	List() []ProviderInfo
	Get(id provider.ID) (ProviderInfo, bool)
}

type ProviderTester interface {
	TestProvider(
		ctx context.Context,
		id provider.ID,
	) error
}

type ProviderHandler struct {
	registry ProviderRegistry
	tester   ProviderTester
	timeout  time.Duration
}

func NewProviderHandler(registry ProviderRegistry, tester ProviderTester, timeout time.Duration) *ProviderHandler {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	return &ProviderHandler{
		registry: registry,
		tester:   tester,
		timeout:  timeout,
	}
}

func (h *ProviderHandler) RegisterRoutes(group *gin.RouterGroup) {
	providers := group.Group("/providers")

	providers.GET("", h.List)
	providers.GET("/:id", h.Get)
	providers.POST("/:id/test", h.Test)
}

func (h *ProviderHandler) List(c *gin.Context) {
	items := h.registry.List()

	response := dto.ProviderListResponse{
		Data: make([]dto.ProviderResponse, 0, len(items)),
	}

	for _, item := range items {
		response.Data = append(
			response.Data,
			toProviderResponse(item),
		)
	}

	c.JSON(http.StatusOK, response)
}

func (h *ProviderHandler) Get(c *gin.Context) {
	id := provider.ID(c.Param("id"))

	item, ok := h.registry.Get(id)
	if !ok {
		writeAPIError(
			c,
			http.StatusNotFound,
			"provider_not_found",
			"Provider does not exist",
		)
		return
	}

	c.JSON(http.StatusOK, toProviderResponse(item))
}

func (h *ProviderHandler) Test(c *gin.Context) {
	id := provider.ID(c.Param("id"))

	item, ok := h.registry.Get(id)
	if !ok {
		writeAPIError(
			c,
			http.StatusNotFound,
			"provider_not_found",
			"Provider does not exist",
		)
		return
	}

	if item.Status != "enabled" {
		writeAPIError(
			c,
			http.StatusConflict,
			"provider_disabled",
			"Provider is not enabled",
		)
		return
	}

	if h.tester == nil {
		writeAPIError(
			c,
			http.StatusNotImplemented,
			"provider_test_unavailable",
			"Provider connectivity test is not configured",
		)
		return
	}

	ctx, cancel := context.WithTimeout(
		c.Request.Context(),
		h.timeout,
	)
	defer cancel()

	start := time.Now()

	err := h.tester.TestProvider(ctx, id)

	latency := time.Since(start).Milliseconds()

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeAPIError(
				c,
				http.StatusGatewayTimeout,
				"provider_test_timeout",
				"Provider connectivity test timed out",
			)
			return
		}

		c.JSON(http.StatusOK, dto.ProviderTestResponse{
			Success:   false,
			Message:   err.Error(),
			LatencyMS: latency,
		})
		return
	}

	c.JSON(http.StatusOK, dto.ProviderTestResponse{
		Success:   true,
		Message:   "Provider connection successful",
		LatencyMS: latency,
	})
}

func toProviderResponse(
	item ProviderInfo,
) dto.ProviderResponse {
	return dto.ProviderResponse{
		ID:      string(item.ID),
		Name:    item.Name,
		Type:    item.Type,
		Status:  item.Status,
		BaseURL: item.BaseURL,
	}
}

func writeAPIError(
	c *gin.Context,
	status int,
	code string,
	message string,
) {
	c.JSON(status, dto.APIErrorResponse{
		Code:    code,
		Message: message,
	})
}
