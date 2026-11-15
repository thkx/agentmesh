package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/thkx/agentmesh/internal/app"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "configuration file path")
	flag.Parse()

	signalCtx, stop := app.SignalContext()
	defer stop()

	container, err := app.Bootstrap(signalCtx, *configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "AgentMesh startup failed: %v\n", err)
		os.Exit(1)
	}

	addr := net.JoinHostPort(container.Config.Server.Host, fmt.Sprintf("%d", container.Config.Server.Port))
	server := &http.Server{
		Addr: addr, Handler: container.HTTPHandler,
		ReadTimeout:       container.Config.Server.ReadTimeout,
		WriteTimeout:      0, // Keep SSE streams alive; request cancellation and Gateway timeout bound execution.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		container.Logger.InfoContext(signalCtx, "AgentMesh HTTP server starting", "address", addr)
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case <-signalCtx.Done():
		container.Logger.Info("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			container.Logger.Error("HTTP server failed", "error", err)
		}
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), container.Config.Server.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		container.Logger.Error("HTTP server shutdown failed", "error", err)
	}
	if err := app.Shutdown(container, container.Config.Server.ShutdownTimeout); err != nil {
		container.Logger.Error("application shutdown failed", "error", err)
		os.Exit(1)
	}
}
