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
	t := Task{ID: req.ID, Title: req.Title, RepositoryPath: req.RepositoryPath, ModelID: req.ModelID, Prompt: req.Prompt, Status: StatusPending, TestCommand: req.TestCommand, MaxRepairAttempts: req.MaxRepairAttempts, TimeoutSeconds: req.TimeoutSeconds, CreatedAt: now, UpdatedAt: now}
	if t.MaxRepairAttempts == 0 {
		t.MaxRepairAttempts = 2
	}
	if t.TimeoutSeconds == 0 {
		t.TimeoutSeconds = 1800
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO coding_tasks(id,title,repository_path,model_id,prompt,status,test_command_json,max_repair_attempts,timeout_seconds,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, t.ID, t.Title, t.RepositoryPath, t.ModelID, t.Prompt, t.Status, string(cmd), t.MaxRepairAttempts, t.TimeoutSeconds, now.Unix(), now.Unix())
	if err != nil {
		return Task{}, fmt.Errorf("create coding task: %w", err)
	}
	_ = r.Event(ctx, t.ID, "task.created", map[string]any{"status": t.Status})
	return t, nil
}

const taskColumns = `id,title,repository_path,model_id,prompt,status,workspace_id,workspace_path,branch_name,test_command_json,max_repair_attempts,repair_attempts,timeout_seconds,result_json,error,created_at,updated_at,started_at,finished_at`

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var status string
	var cmd, result string
	var created, updated, started, finished int64
	err := row.Scan(&t.ID, &t.Title, &t.RepositoryPath, &t.ModelID, &t.Prompt, &status, &t.WorkspaceID, &t.WorkspacePath, &t.BranchName, &cmd, &t.MaxRepairAttempts, &t.RepairAttempts, &t.TimeoutSeconds, &result, &t.Error, &created, &updated, &started, &finished)
	if err != nil {
		return Task{}, err
	}
	t.Status = Status(status)
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
	_, e := r.db.ExecContext(ctx, `UPDATE coding_tasks SET status=?,workspace_id=?,workspace_path=?,branch_name=?,repair_attempts=?,result_json=?,error=?,updated_at=?,started_at=?,finished_at=? WHERE id=?`, t.Status, t.WorkspaceID, t.WorkspacePath, t.BranchName, t.RepairAttempts, string(resultJSON), t.Error, time.Now().UTC().Unix(), unix(t.StartedAt), unix(t.FinishedAt), t.ID)
	return e
}
func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
func (r *Repository) Event(ctx context.Context, taskID, kind string, payload any) error {
	b, e := json.Marshal(payload)
	if e != nil {
		return e
	}
	_, e = r.db.ExecContext(ctx, `INSERT INTO coding_task_events(id,task_id,event_type,payload_json,created_at) VALUES(?,?,?,?,?)`, uuid.NewString(), taskID, kind, string(b), time.Now().UTC().Unix())
	return e
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
	rows, e := r.db.QueryContext(ctx, `SELECT id,task_id,event_type,payload_json,created_at FROM coding_task_events WHERE task_id=? ORDER BY created_at,id`, id)
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

// RecoverInterrupted marks in-flight tasks as failed after an unclean process stop.
// Worktrees are intentionally retained for inspection and manual recovery.
func (r *Repository) RecoverInterrupted(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Unix()
	result, err := r.db.ExecContext(ctx, `UPDATE coding_tasks SET status='failed',error='interrupted by AgentMesh restart',updated_at=?,finished_at=? WHERE status IN ('running','testing','repairing')`, now, now)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
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
