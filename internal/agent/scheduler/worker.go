package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"
)

var workerSequence atomic.Uint64

type WorkerConfig struct {
	Logger        *slog.Logger
	ID            string
	PollInterval  time.Duration
	LeaseDuration time.Duration
}
type Worker struct {
	logger  *slog.Logger
	store   Store
	handler Handler
	cfg     WorkerConfig
}

func NewWorker(store Store, handler Handler, cfg WorkerConfig) (*Worker, error) {
	if store == nil || handler == nil {
		return nil, errors.New("scheduler worker dependency is nil")
	}
	host, _ := os.Hostname()
	sequence := workerSequence.Add(1)
	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("%s-%d-%d", host, os.Getpid(), sequence)
	} else {
		cfg.ID = fmt.Sprintf("%s@%s-%d-%d", cfg.ID, host, os.Getpid(), sequence)
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 30 * time.Second
	}
	if cfg.LeaseDuration < 3*time.Second {
		return nil, errors.New("scheduler lease duration must be at least 3 seconds")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{logger: logger, store: store, handler: handler, cfg: cfg}, nil
}
func (w *Worker) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("scheduler context is nil")
	}
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	for {
		if requeued, interrupted, err := w.store.RecoverExpired(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.logger.ErrorContext(ctx, "recover scheduler leases", "error", err)
		} else if requeued > 0 || interrupted > 0 {
			w.logger.WarnContext(ctx, "scheduler leases recovered", "requeued", requeued, "interrupted", interrupted)
		}
		if err := w.runOne(ctx); err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, context.Canceled) {
			w.logger.ErrorContext(ctx, "scheduler worker iteration failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (w *Worker) runOne(ctx context.Context) error {
	job, err := w.store.Claim(ctx, w.cfg.ID, w.cfg.LeaseDuration)
	if err != nil {
		return err
	}
	if err = w.store.MarkStarted(ctx, job.ID, w.cfg.ID); err != nil {
		return err
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatDone := make(chan struct{})
	go w.heartbeat(workCtx, cancel, job.ID, heartbeatDone)
	defer func() { cancel(); <-heartbeatDone }()
	_, runErr := w.handler.Run(workCtx, job.Task)
	// During process shutdown leave the lease to expire; startup recovery will mark it interrupted,
	// never replaying an execution that may already have caused side effects.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	if runErr != nil {
		return w.store.Fail(finishCtx, job.ID, w.cfg.ID, runErr)
	}
	return w.store.Complete(finishCtx, job.ID, w.cfg.ID)
}
func (w *Worker) heartbeat(ctx context.Context, cancel context.CancelFunc, id string, done chan<- struct{}) {
	defer close(done)
	interval := w.cfg.LeaseDuration / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hbCtx, hbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := w.store.Heartbeat(hbCtx, id, w.cfg.ID, w.cfg.LeaseDuration)
			hbCancel()
			if err != nil {
				cancel()
				return
			}
		}
	}
}
