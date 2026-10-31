package http

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/thkx/agentmesh/internal/transport/http/handlers"
)

type Dependencies struct {
	ProviderRegistry handlers.ProviderRegistry
	ProviderTester   handlers.ProviderTester
}

func NewRouter(deps Dependencies) *gin.Engine {
	router := gin.New()

	router.Use(gin.Recovery())

	api := router.Group("/api/v1")

	providerHandler := handlers.NewProviderHandler(
		deps.ProviderRegistry,
		deps.ProviderTester,
		15*time.Second,
	)

	providerHandler.RegisterRoutes(api)

	return router
}
