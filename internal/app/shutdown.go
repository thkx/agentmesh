package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
}

func Shutdown(container *Container, timeout time.Duration) error {
	if container == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	done := make(chan error, 1)

	go func() {
		done <- container.Close()
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("close application resources: %w", err)
		}
		return nil
	case <-ctx.Done():
		return errors.Join(
			fmt.Errorf("application shutdown timed out: %w", ctx.Err()),
			containerCloseTimeoutLog(container.Logger),
		)
	}
}

func containerCloseTimeoutLog(log *slog.Logger) error {
	if log != nil {
		log.Error("application shutdown timed out")
	}
	return nil
}
