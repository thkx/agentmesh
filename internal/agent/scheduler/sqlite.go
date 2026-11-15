package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
)

type SQLiteStore struct{ db *sql.DB }

func NewSQLiteStore(db *sql.DB) (*SQLiteStore, error) {
	if db == nil {
		return nil, errors.New("scheduler database is nil")
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Enqueue(ctx context.Context, task dom.Task, id string, maxAttempts int) (Job, error) {
	if err := task.Validate(); err != nil {
		return Job{}, err
	}
	if id == "" {
		id = task.ID
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	raw, err := encodeTask(task)
	if err != nil {
		return Job{}, err
	}
	now := time.Now().UTC()
	if len(id) > 128 || len(task.ID) > 128 {
		return Job{}, errors.New("scheduler job and task IDs must be at most 128 characters")
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO agent_jobs(id,task_id,task_json,status,attempts,max_attempts,available_at,created_at,updated_at) VALUES(?,?,?,'queued',0,?,?,?,?)`, id, task.ID, raw, maxAttempts, now.UnixNano(), now.UnixNano(), now.UnixNano())
	if err != nil {
		existing, lookupErr := s.Get(ctx, id)
		if lookupErr == nil && existing.Task.ID == task.ID {
			old, _ := encodeTask(existing.Task)
			if old == raw {
				return existing, nil
			}
		}
		return Job{}, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return s.Get(ctx, id)
}
func (s *SQLiteStore) Claim(ctx context.Context, owner string, lease time.Duration) (Job, error) {
	if owner == "" || lease <= 0 {
		return Job{}, errors.New("invalid lease owner or duration")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM agent_jobs WHERE status='queued' AND available_at<=? AND attempts<max_attempts ORDER BY available_at,created_at LIMIT 1`, time.Now().UTC().UnixNano()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	now := time.Now().UTC()
	until := now.Add(lease)
	res, err := tx.ExecContext(ctx, `UPDATE agent_jobs SET status='running',attempts=attempts+1,lease_owner=?,lease_until=?,execution_started=0,updated_at=? WHERE id=? AND status='queued'`, owner, until.UnixNano(), now.UnixNano(), id)
	if err != nil {
		return Job{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Job{}, ErrNotFound
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}
func (s *SQLiteStore) MarkStarted(ctx context.Context, id, owner string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agent_jobs SET execution_started=1,updated_at=? WHERE id=? AND status='running' AND lease_owner=? AND lease_until>?`, time.Now().UTC().UnixNano(), id, owner, time.Now().UTC().UnixNano())
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (s *SQLiteStore) Heartbeat(ctx context.Context, id, owner string, lease time.Duration) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `UPDATE agent_jobs SET lease_until=?,updated_at=? WHERE id=? AND status='running' AND lease_owner=? AND lease_until>?`, now.Add(lease).UnixNano(), now.UnixNano(), id, owner, now.UnixNano())
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (s *SQLiteStore) Complete(ctx context.Context, id, owner string) error {
	return s.finish(ctx, id, owner, Succeeded, nil)
}
func (s *SQLiteStore) Fail(ctx context.Context, id, owner string, cause error) error {
	if cause == nil {
		cause = errors.New("agent execution failed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var taskID string
	var attempts, maxAttempts int
	if err = tx.QueryRowContext(ctx, `SELECT task_id,attempts,max_attempts FROM agent_jobs WHERE id=? AND status='running' AND lease_owner=?`, id, owner).Scan(&taskID, &attempts, &maxAttempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		return err
	}
	var sideEffects int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_tool_invocations WHERE task_id=?`, taskID).Scan(&sideEffects); err != nil {
		return err
	}
	now := time.Now().UTC()
	msg := cause.Error()
	if len(msg) > 2048 {
		msg = msg[:2048]
	}
	status := Failed
	available := now.UnixNano()
	// Explicit cancellation and task timeouts are terminal; retry only transient non-context failures.
	if errors.Is(cause, context.Canceled) {
		status = Canceled
	}
	if sideEffects == 0 && attempts < maxAttempts && !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		status = Queued
		delay := time.Duration(1<<min(attempts, 8)) * time.Second
		available = now.Add(delay).UnixNano()
	}
	_, err = tx.ExecContext(ctx, `UPDATE agent_jobs SET status=?,last_error=?,available_at=?,lease_owner=NULL,lease_until=NULL,updated_at=? WHERE id=? AND status='running' AND lease_owner=?`, status, msg, available, now.UnixNano(), id, owner)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *SQLiteStore) finish(ctx context.Context, id, owner string, status Status, cause error) error {
	now := time.Now().UTC()
	msg := ""
	if cause != nil {
		msg = cause.Error()
		if len(msg) > 2048 {
			msg = msg[:2048]
		}
	}
	res, err := s.db.ExecContext(ctx, `UPDATE agent_jobs SET status=?,last_error=?,lease_owner=NULL,lease_until=NULL,updated_at=? WHERE id=? AND status='running' AND lease_owner=?`, status, msg, now.UnixNano(), id, owner)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (s *SQLiteStore) Get(ctx context.Context, id string) (Job, error) {
	var j Job
	var raw, status string
	var available, created, updated int64
	var until sql.NullInt64
	var owner, lastErr sql.NullString
	var started int
	err := s.db.QueryRowContext(ctx, `SELECT id,task_json,status,attempts,max_attempts,available_at,lease_owner,lease_until,execution_started,last_error,created_at,updated_at FROM agent_jobs WHERE id=?`, id).Scan(&j.ID, &raw, &status, &j.Attempts, &j.MaxAttempts, &available, &owner, &until, &started, &lastErr, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	if err = json.Unmarshal([]byte(raw), &j.Task); err != nil {
		return Job{}, fmt.Errorf("decode queued task: %w", err)
	}
	j.Status = Status(status)
	j.AvailableAt = time.Unix(0, available).UTC()
	j.CreatedAt = time.Unix(0, created).UTC()
	j.UpdatedAt = time.Unix(0, updated).UTC()
	j.ExecutionStarted = started != 0
	if owner.Valid {
		j.LeaseOwner = owner.String
	}
	if until.Valid {
		v := time.Unix(0, until.Int64).UTC()
		j.LeaseUntil = &v
	}
	if lastErr.Valid {
		j.LastError = lastErr.String
	}
	return j, nil
}

// RecoverExpired retries only known-safe work; ambiguous in-flight executions are interrupted.
func (s *SQLiteStore) RecoverExpired(ctx context.Context) (requeued, interrupted int64, err error) {
	now := time.Now().UTC().UnixNano()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return 0, 0, e
	}
	defer tx.Rollback()
	// A claimed job that never crossed the execution boundary is safe to retry.
	r, e := tx.ExecContext(ctx, `UPDATE agent_jobs SET status='queued',lease_owner=NULL,lease_until=NULL,available_at=?,updated_at=? WHERE status='running' AND lease_until<=? AND execution_started=0 AND attempts<max_attempts`, now+int64(2*time.Second), now, now)
	if e != nil {
		return 0, 0, e
	}
	preStart, _ := r.RowsAffected()
	// If Runtime durably recorded a failed task and no tool intent exists, it is safe to retry.
	r, e = tx.ExecContext(ctx, `UPDATE agent_jobs SET status='queued',lease_owner=NULL,lease_until=NULL,available_at=?,updated_at=? WHERE status='running' AND lease_until<=? AND execution_started=1 AND attempts<max_attempts AND EXISTS (SELECT 1 FROM agent_task_states st WHERE st.task_id=agent_jobs.task_id AND json_valid(st.state_json) AND json_extract(st.state_json,'$.result.status')='failed' AND COALESCE(json_extract(st.state_json,'$.result.tool_calls'),0)=0) AND NOT EXISTS (SELECT 1 FROM agent_tool_invocations ti WHERE ti.task_id=agent_jobs.task_id)`, now+int64(2*time.Second), now, now)
	if e != nil {
		return 0, 0, e
	}
	failedSafe, _ := r.RowsAffected()
	requeued = preStart + failedSafe
	// Reconcile the crash window after Runtime persisted success but before the queue ack.
	_, e = tx.ExecContext(ctx, `UPDATE agent_jobs SET status='succeeded',lease_owner=NULL,lease_until=NULL,last_error=NULL,updated_at=? WHERE status='running' AND lease_until<=? AND execution_started=1 AND EXISTS (SELECT 1 FROM agent_task_states st WHERE st.task_id=agent_jobs.task_id AND json_valid(st.state_json) AND json_extract(st.state_json,'$.result.status')='succeeded')`, now, now)
	if e != nil {
		return 0, 0, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE agent_jobs SET status='canceled',lease_owner=NULL,lease_until=NULL,last_error='task canceled before worker acknowledgement',updated_at=? WHERE status='running' AND lease_until<=? AND execution_started=1 AND EXISTS (SELECT 1 FROM agent_task_states st WHERE st.task_id=agent_jobs.task_id AND json_valid(st.state_json) AND json_extract(st.state_json,'$.result.status')='canceled')`, now, now)
	if e != nil {
		return 0, 0, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE agent_jobs SET status='failed',lease_owner=NULL,lease_until=NULL,last_error='task timed out before worker acknowledgement',updated_at=? WHERE status='running' AND lease_until<=? AND execution_started=1 AND EXISTS (SELECT 1 FROM agent_task_states st WHERE st.task_id=agent_jobs.task_id AND json_valid(st.state_json) AND json_extract(st.state_json,'$.result.status')='timed_out')`, now, now)
	if e != nil {
		return 0, 0, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE agent_jobs SET status='failed',lease_owner=NULL,lease_until=NULL,last_error='maximum attempts exhausted before execution started',updated_at=? WHERE status='running' AND lease_until<=? AND execution_started=0 AND attempts>=max_attempts`, now, now)
	if e != nil {
		return 0, 0, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE agent_jobs SET status='failed',lease_owner=NULL,lease_until=NULL,last_error='maximum attempts exhausted after a safe model-only failure',updated_at=? WHERE status='running' AND lease_until<=? AND execution_started=1 AND attempts>=max_attempts AND EXISTS (SELECT 1 FROM agent_task_states st WHERE st.task_id=agent_jobs.task_id AND json_valid(st.state_json) AND json_extract(st.state_json,'$.result.status')='failed' AND COALESCE(json_extract(st.state_json,'$.result.tool_calls'),0)=0) AND NOT EXISTS (SELECT 1 FROM agent_tool_invocations ti WHERE ti.task_id=agent_jobs.task_id)`, now, now)
	if e != nil {
		return 0, 0, e
	}
	// Unknown in-flight outcomes are terminally interrupted: never replay potential side effects.
	r, e = tx.ExecContext(ctx, `UPDATE agent_jobs SET status='interrupted',lease_owner=NULL,lease_until=NULL,last_error='worker lease expired after execution started; automatic replay prevented',updated_at=? WHERE status='running' AND lease_until<=? AND execution_started=1`, now, now)
	if e != nil {
		return 0, 0, e
	}
	interrupted, _ = r.RowsAffected()
	if e = tx.Commit(); e != nil {
		return 0, 0, e
	}
	return requeued, interrupted, nil
}

// Requeue permits an operator retry only when no tool invocation intent was recorded.
func (s *SQLiteStore) Requeue(ctx context.Context, id string) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	var taskID string
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT task_id,status FROM agent_jobs WHERE id=?`, id).Scan(&taskID, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, ErrNotFound
		}
		return Job{}, err
	}
	var sideEffects int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_tool_invocations WHERE task_id=?`, taskID).Scan(&sideEffects); err != nil {
		return Job{}, err
	}
	var stateRaw sql.NullString
	stateErr := tx.QueryRowContext(ctx, `SELECT state_json FROM agent_task_states WHERE task_id=?`, taskID).Scan(&stateRaw)
	if stateErr != nil && !errors.Is(stateErr, sql.ErrNoRows) {
		return Job{}, stateErr
	}
	if status != string(Failed) || sideEffects > 0 {
		return Job{}, ErrAlreadyStarted
	}
	if stateRaw.Valid {
		var saved struct {
			Result struct {
				Status    string `json:"status"`
				ToolCalls int    `json:"tool_calls"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(stateRaw.String), &saved); err != nil {
			return Job{}, fmt.Errorf("decode task state for retry: %w", err)
		}
		if saved.Result.Status != "failed" || saved.Result.ToolCalls != 0 {
			return Job{}, ErrAlreadyStarted
		}
	}
	now := time.Now().UTC().UnixNano()
	_, err = tx.ExecContext(ctx, `UPDATE agent_jobs SET status='queued',attempts=0,available_at=?,lease_owner=NULL,lease_until=NULL,execution_started=0,last_error=NULL,updated_at=? WHERE id=?`, now, now, id)
	if err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *SQLiteStore) CancelTask(ctx context.Context, taskID string) error {
	now := time.Now().UTC().UnixNano()
	res, err := s.db.ExecContext(ctx, `UPDATE agent_jobs SET status='canceled',last_error='canceled before execution started',updated_at=? WHERE task_id=? AND status='queued'`, now, taskID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrAlreadyStarted
	}
	return nil
}
