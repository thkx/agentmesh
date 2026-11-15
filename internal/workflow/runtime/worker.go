package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	wf "github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/workflow/checkpoint"
)

var workerSeq atomic.Uint64

type WorkerConfig struct {
	ID            string
	PollInterval  time.Duration
	LeaseDuration time.Duration
	Logger        *slog.Logger
}
type Worker struct {
	store  QueueStore
	engine *Engine
	cfg    WorkerConfig
	logger *slog.Logger
}

func NewWorker(store QueueStore, engine *Engine, cfg WorkerConfig) (*Worker, error) {
	if store == nil || engine == nil {
		return nil, errors.New("workflow worker dependency is nil")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.LeaseDuration < 3*time.Second {
		cfg.LeaseDuration = 30 * time.Second
	}
	if cfg.ID == "" {
		host, _ := os.Hostname()
		cfg.ID = fmt.Sprintf("%s-%d-%d", host, os.Getpid(), workerSeq.Add(1))
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Worker{store: store, engine: engine, cfg: cfg, logger: cfg.Logger}, nil
}
func (w *Worker) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("workflow worker context is nil")
	}
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	for {
		if err := w.runOne(ctx); err != nil && !errors.Is(err, checkpoint.ErrNodeIndeterminate) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, errNoWorkflowJob) {
			w.logger.ErrorContext(ctx, "workflow worker iteration failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

var errNoWorkflowJob = errors.New("no workflow job available")

func (w *Worker) runOne(ctx context.Context) error {
	job, err := w.store.ClaimRun(ctx, w.cfg.ID, w.cfg.LeaseDuration)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errNoWorkflowJob
		}
		return err
	}
	workCtx, cancel := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		interval := w.cfg.LeaseDuration / 3
		if interval < time.Second {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-workCtx.Done():
				return
			case <-ticker.C:
				if e := w.store.HeartbeatRun(workCtx, job, w.cfg.LeaseDuration); e != nil {
					w.logger.ErrorContext(workCtx, "workflow lease heartbeat failed", "run_id", job.RunID, "error", e)
					cancel()
					return
				}
			}
		}
	}()
	run, runErr := w.engine.ProcessLeased(workCtx, job.RunID, job.Owner, job.FenceToken, w.cfg.LeaseDuration)
	cancel()
	<-heartbeatDone
	if errors.Is(runErr, context.Canceled) && ctx.Err() != nil {
		return runErr
	}
	if run.Status == wf.RunWaitingApproval && runErr == nil {
		return w.store.FinishRun(ctx, job, run, nil)
	}
	if finishErr := w.store.FinishRun(context.Background(), job, run, runErr); finishErr != nil {
		return finishErr
	}
	return runErr
}
