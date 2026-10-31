package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/thkx/agentmesh/internal/app"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "configuration file path")
	flag.Parse()

	ctx, stop := app.SignalContext()
	defer stop()

	container, err := app.Bootstrap(ctx, *configPath)
	if err != nil {
		log.Printf("AgentMesh startup failed: %v", err)
		os.Exit(1)
	}

	container.Logger.InfoContext(ctx, "AgentMesh is ready")

	<-ctx.Done()

	container.Logger.Info("shutdown signal received")

	if err := app.Shutdown(container, container.Config.Server.ShutdownTimeout); err != nil {
		container.Logger.Error("shutdown failed", "error", err)
		os.Exit(1)
	}

	container.Logger.Info("AgentMesh stopped")
}

var _ context.Context
