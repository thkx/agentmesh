package coding

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("coding repository database is nil")
	}
	return &Repository{db: db}, nil
}
func (r *Repository) Create(ctx context.Context, req CreateRequest) (Task, error) {
	now := time.Now().UTC()
	cmd, _ := json.Marshal(req.TestCommand)
	t := Task{ID: req.ID, ExecutionID: req.ExecutionID, Title: req.Title, RepositoryPath: req.RepositoryPath, ModelID: req.ModelID, Prompt: req.Prompt, Status: StatusPending, TestCommand: req.TestCommand, MaxRepairAttempts: req.MaxRepairAttempts, TimeoutSeconds: req.TimeoutSeconds, CreatedAt: now, UpdatedAt: now}
	if t.MaxRepairAttempts == 0 {
		t.MaxRepairAttempts = 2
	}
	if t.TimeoutSeconds == 0 {
		t.TimeoutSeconds = 1800
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO coding_tasks(id,title,repository_path,model_id,prompt,status,test_command_json,max_repair_attempts,timeout_seconds,created_at,updated_at,execution_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, t.ID, t.Title, t.RepositoryPath, t.ModelID, t.Prompt, t.Status, string(cmd), t.MaxRepairAttempts, t.TimeoutSeconds, now.Unix(), now.Unix(), t.ExecutionID)
	if err != nil {
		return Task{}, fmt.Errorf("create coding task: %w", err)
	}
	if err = insertEvent(ctx, tx, t.ID, "task.created", map[string]any{"status": t.Status}); err != nil {
		return Task{}, fmt.Errorf("persist task.created event: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return Task{}, err
	}
	return t, nil
}

const taskColumns = `id,title,repository_path,model_id,prompt,status,workspace_id,workspace_path,branch_name,test_command_json,max_repair_attempts,repair_attempts,timeout_seconds,result_json,error,created_at,updated_at,started_at,finished_at,execution_id,outcome_unknown`

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var status string
	var cmd, result string
	var created, updated, started, finished int64
	var unknown int
	err := row.Scan(&t.ID, &t.Title, &t.RepositoryPath, &t.ModelID, &t.Prompt, &status, &t.WorkspaceID, &t.WorkspacePath, &t.BranchName, &cmd, &t.MaxRepairAttempts, &t.RepairAttempts, &t.TimeoutSeconds, &result, &t.Error, &created, &updated, &started, &finished, &t.ExecutionID, &unknown)
	if err != nil {
		return Task{}, err
	}
	t.Status = Status(status)
	t.OutcomeUnknown = unknown != 0
	if t.OutcomeUnknown { t.Status = StatusInterrupted }
	_ = json.Unmarshal([]byte(cmd), &t.TestCommand)
	if json.Unmarshal([]byte(result), &t.Result) != nil {
		t.Result = result
	}
	t.CreatedAt = time.Unix(created, 0).UTC()
	t.UpdatedAt = time.Unix(updated, 0).UTC()
	if started > 0 {
		t.StartedAt = time.Unix(started, 0).UTC()
	}
	if finished > 0 {
		t.FinishedAt = time.Unix(finished, 0).UTC()
	}
	return t, nil
}
func (r *Repository) Get(ctx context.Context, id string) (Task, error) {
	t, e := scanTask(r.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM coding_tasks WHERE id=?`, id))
	if errors.Is(e, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return t, e
}
func (r *Repository) List(ctx context.Context, limit int) ([]Task, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, e := r.db.QueryContext(ctx, `SELECT `+taskColumns+` FROM coding_tasks ORDER BY created_at DESC LIMIT ?`, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (r *Repository) Update(ctx context.Context, t Task) error {
	resultJSON, _ := json.Marshal(t.Result)
	_, e := r.db.ExecContext(ctx, `UPDATE coding_tasks SET status=?,workspace_id=?,workspace_path=?,branch_name=?,repair_attempts=?,result_json=?,error=?,updated_at=?,started_at=?,finished_at=?,execution_id=?,outcome_unknown=? WHERE id=?`, persistedStatus(t), t.WorkspaceID, t.WorkspacePath, t.BranchName, t.RepairAttempts, string(resultJSON), t.Error, time.Now().UTC().Unix(), unix(t.StartedAt), unix(t.FinishedAt), t.ExecutionID, boolInt(t.OutcomeUnknown), t.ID)
	return e
}
func persistedStatus(t Task) Status { if t.OutcomeUnknown { return StatusFailed }; return t.Status }
func boolInt(v bool) int { if v { return 1 }; return 0 }
func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

type eventExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertEvent(ctx context.Context, execer eventExecer, taskID, kind string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = execer.ExecContext(ctx, `INSERT INTO coding_task_events(id,task_id,event_type,payload_json,created_at) VALUES(?,?,?,?,?)`, uuid.NewString(), taskID, kind, string(b), time.Now().UTC().Unix())
	return err
}
func (r *Repository) Event(ctx context.Context, taskID, kind string, payload any) error {
	return insertEvent(ctx, r.db, taskID, kind, payload)
}

// ClaimWithEvent commits the claim and its event atomically.
func (r *Repository) ClaimWithEvent(ctx context.Context, id, kind string, payload any) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `UPDATE coding_tasks SET status='running',started_at=?,finished_at=0,error='',updated_at=? WHERE id=? AND status='pending'`, now, now, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	if err = insertEvent(ctx, tx, id, kind, payload); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// UpdateWithEvent atomically persists task state and the corresponding domain event.
func (r *Repository) UpdateWithEvent(ctx context.Context, t Task, kind string, payload any) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	resultJSON, _ := json.Marshal(t.Result)
	result, err := tx.ExecContext(ctx, `UPDATE coding_tasks SET status=?,workspace_id=?,workspace_path=?,branch_name=?,repair_attempts=?,result_json=?,error=?,updated_at=?,started_at=?,finished_at=?,execution_id=?,outcome_unknown=? WHERE id=?`, persistedStatus(t), t.WorkspaceID, t.WorkspacePath, t.BranchName, t.RepairAttempts, string(resultJSON), t.Error, time.Now().UTC().Unix(), unix(t.StartedAt), unix(t.FinishedAt), t.ExecutionID, boolInt(t.OutcomeUnknown), t.ID)
	if err != nil {
		return err
	}
	if n, rowsErr := result.RowsAffected(); rowsErr != nil {
		return rowsErr
	} else if n != 1 {
		return ErrNotFound
	}
	if err = insertEvent(ctx, tx, t.ID, kind, payload); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *Repository) AddStep(ctx context.Context, s Step) error {
	inputJSON := encodeJSONText(s.Input)
	outputJSON := encodeJSONText(s.Output)
	_, e := r.db.ExecContext(ctx, `INSERT INTO coding_task_steps(id,task_id,sequence,kind,status,input_json,output_json,error,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), s.TaskID, s.Sequence, s.Kind, s.Status, inputJSON, outputJSON, s.Error, unix(s.StartedAt), unix(s.FinishedAt))
	return e
}
func encodeJSONText(s string) string {
	if s == "" {
		return "{}"
	}
	if json.Valid([]byte(s)) {
		return s
	}
	b, _ := json.Marshal(s)
	return string(b)
}
func (r *Repository) Steps(ctx context.Context, id string) ([]Step, error) {
	rows, e := r.db.QueryContext(ctx, `SELECT id,task_id,sequence,kind,status,input_json,output_json,error,started_at,finished_at FROM coding_task_steps WHERE task_id=? ORDER BY sequence`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Step{}
	for rows.Next() {
		var s Step
		var inputJSON, outputJSON string
		var a, b int64
		if e = rows.Scan(&s.ID, &s.TaskID, &s.Sequence, &s.Kind, &s.Status, &inputJSON, &outputJSON, &s.Error, &a, &b); e != nil {
			return nil, e
		}
		if json.Unmarshal([]byte(inputJSON), &s.Input) != nil {
			s.Input = inputJSON
		}
		if json.Unmarshal([]byte(outputJSON), &s.Output) != nil {
			s.Output = outputJSON
		}
		s.StartedAt = time.Unix(a, 0).UTC()
		if b > 0 {
			s.FinishedAt = time.Unix(b, 0).UTC()
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) SaveSnapshot(ctx context.Context, s Snapshot) error {
	_, e := r.db.ExecContext(ctx, `INSERT INTO coding_task_snapshots(id,task_id,label,commit_hash,diff,created_at) VALUES(?,?,?,?,?,?)`, uuid.NewString(), s.TaskID, s.Label, s.CommitHash, s.Diff, time.Now().UTC().Unix())
	return e
}

func (r *Repository) Events(ctx context.Context, id string) ([]Event, error) {
	// coding_task_events is an append-only SQLite event log. created_at currently
	// has second precision, so ordering by (created_at, id) can reorder events
	// created in the same second because IDs are random UUIDs. rowid preserves
	// the actual insertion order for this rowid-backed table.
	rows, e := r.db.QueryContext(ctx, `SELECT id,task_id,event_type,payload_json,created_at FROM coding_task_events WHERE task_id=? ORDER BY rowid`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var v Event
		var ts int64
		if e = rows.Scan(&v.ID, &v.TaskID, &v.Type, &v.Payload, &ts); e != nil {
			return nil, e
		}
		v.CreatedAt = time.Unix(ts, 0).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

// RecoverInterrupted preserves uncertainty and appends a task event atomically.
// The legacy CHECK constraint stores physical status as failed; outcome_unknown
// exposes the logical interrupted state without rewriting the original schema.
func (r *Repository) RecoverInterrupted(ctx context.Context) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil { return 0, err }
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM coding_tasks WHERE status IN ('running','testing','repairing') AND outcome_unknown=0`)
	if err != nil { return 0, err }
	ids := []string{}
	for rows.Next() { var id string; if err = rows.Scan(&id); err != nil { rows.Close(); return 0, err }; ids = append(ids, id) }
	if err = rows.Err(); err != nil { rows.Close(); return 0, err }
	if err = rows.Close(); err != nil { return 0, err }
	now := time.Now().UTC().Unix()
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `UPDATE coding_tasks SET status='failed',outcome_unknown=1,error='interrupted by AgentMesh restart; inspect retained worktree before resolving',updated_at=?,finished_at=? WHERE id=?`, now, now, id); err != nil { return 0, err }
		if err = insertEvent(ctx, tx, id, "task.interrupted", map[string]string{"reason":"process restarted before outcome was confirmed"}); err != nil { return 0, err }
	}
	if err = tx.Commit(); err != nil { return 0, err }
	return int64(len(ids)), nil
}

// BindExecution makes the Coding Task ↔ canonical Execution relation durable.
func (r *Repository) BindExecution(ctx context.Context, taskID, executionID string) error {
	if taskID == "" || executionID == "" { return ErrInvalidTask }
	result, err := r.db.ExecContext(ctx, `UPDATE coding_tasks SET execution_id=?,updated_at=? WHERE id=? AND (execution_id='' OR execution_id=?)`, executionID, time.Now().UTC().Unix(), taskID, executionID)
	if err != nil { return err }
	n, err := result.RowsAffected()
	if err != nil { return err }
	if n == 1 { return nil }
	var existing string
	if err = r.db.QueryRowContext(ctx, `SELECT execution_id FROM coding_tasks WHERE id=?`, taskID).Scan(&existing); errors.Is(err, sql.ErrNoRows) { return ErrNotFound }
	if err != nil { return err }
	if existing != executionID { return ErrConflict }
	return nil
}

func (r *Repository) Claim(ctx context.Context, id string) (bool, error) {
	now := time.Now().UTC().Unix()
	result, err := r.db.ExecContext(ctx, `UPDATE coding_tasks SET status='running',started_at=?,finished_at=0,error='',updated_at=? WHERE id=? AND status='pending'`, now, now, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (r *Repository) Snapshots(ctx context.Context, taskID string) ([]Snapshot, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,task_id,label,commit_hash,diff,created_at FROM coding_task_snapshots WHERE task_id=? ORDER BY created_at,id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snapshot{}
	for rows.Next() {
		var v Snapshot
		var ts int64
		if err = rows.Scan(&v.ID, &v.TaskID, &v.Label, &v.CommitHash, &v.Diff, &ts); err != nil {
			return nil, err
		}
		v.CreatedAt = time.Unix(ts, 0).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

// CancelPending atomically cancels a task that has not been claimed by a worker.
// The state transition and event are committed together so a racing worker either
// claims the task first or observes that the task is no longer pending.
func (r *Repository) CancelPending(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `UPDATE coding_tasks SET status='canceled',error='canceled before execution',updated_at=?,finished_at=? WHERE id=? AND status='pending'`, now, now, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		var exists int
		queryErr := tx.QueryRowContext(ctx, `SELECT 1 FROM coding_tasks WHERE id=?`, id).Scan(&exists)
		if errors.Is(queryErr, sql.ErrNoRows) {
			return ErrNotFound
		}
		if queryErr != nil {
			return queryErr
		}
		return ErrConflict
	}
	if err := insertEvent(ctx, tx, id, "task.canceled", map[string]string{"reason": "canceled before execution"}); err != nil {
		return err
	}
	return tx.Commit()
}
