package checkpoint

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/thkx/agentmesh/internal/domain/workflow"
	"sort"
	"time"
)

type Store struct{ db *sql.DB }

func NewSQLite(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("workflow checkpoint database is nil")
	}
	return &Store{db: db}, nil
}
func (s *Store) SaveDefinition(ctx context.Context, d workflow.Definition) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT definition_json FROM workflows WHERE id=? AND version=?`, d.ID, d.Version).Scan(&existing)
	if err == nil {
		if existing != string(b) {
			return errors.New("workflow version is immutable; increment version to publish changes")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workflows(id,version,name,definition_json,created_at) VALUES(?,?,?,?,?)`, d.ID, d.Version, d.Name, string(b), time.Now().Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetDefinition(ctx context.Context, id string, version int) (workflow.Definition, error) {
	var b string
	var d workflow.Definition
	var e error
	if version > 0 {
		e = s.db.QueryRowContext(ctx, `SELECT definition_json FROM workflows WHERE id=? AND version=?`, id, version).Scan(&b)
	} else {
		e = s.db.QueryRowContext(ctx, `SELECT definition_json FROM workflows WHERE id=? ORDER BY version DESC LIMIT 1`, id).Scan(&b)
	}
	if e != nil {
		return d, e
	}
	e = json.Unmarshal([]byte(b), &d)
	return d, e
}
func (s *Store) CreateRun(ctx context.Context, r workflow.Run) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	insertResult, insertErr := tx.ExecContext(ctx, `INSERT INTO workflow_runs(id,workflow_id,workflow_version,status,current_node,state_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, r.ID, r.WorkflowID, r.WorkflowVersion, r.Status, nullableString(r.CurrentNode), string(b), r.CreatedAt.Unix(), r.UpdatedAt.Unix())
	e = insertErr
	if e != nil {
		return e
	}
	inserted, _ := insertResult.RowsAffected()
	if inserted == 1 {
		if e = appendEventTx(ctx, tx, r.ID, "run.queued", "", "", map[string]any{"workflow_id": r.WorkflowID, "workflow_version": r.WorkflowVersion}); e != nil {
			return e
		}
	}
	var workflowID string
	var version int
	var existingJSON string
	if e = tx.QueryRowContext(ctx, `SELECT workflow_id,workflow_version,state_json FROM workflow_runs WHERE id=?`, r.ID).Scan(&workflowID, &version, &existingJSON); e != nil {
		return e
	}
	if workflowID != r.WorkflowID || version != r.WorkflowVersion {
		return errors.New("workflow run idempotency key already belongs to another workflow")
	}
	var existing workflow.Run
	if e = json.Unmarshal([]byte(existingJSON), &existing); e != nil {
		return e
	}
	if string(existing.Input) != string(r.Input) {
		return errors.New("workflow run idempotency key reused with different input")
	}
	return tx.Commit()
}
func (s *Store) SaveRun(ctx context.Context, r workflow.Run) error {
	return s.saveRun(ctx, r, "", 0, false)
}

// SaveRunFenced rejects checkpoint writes from workers whose lease generation is stale.
func (s *Store) SaveRunFenced(ctx context.Context, r workflow.Run, owner string, fenceToken int64) error {
	if owner == "" || fenceToken < 1 {
		return errors.New("invalid workflow fencing identity")
	}
	return s.saveRun(ctx, r, owner, fenceToken, true)
}

func (s *Store) saveRun(ctx context.Context, r workflow.Run, owner string, fenceToken int64, fenced bool) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldJSON string
	if err = tx.QueryRowContext(ctx, `SELECT state_json FROM workflow_runs WHERE id=?`, r.ID).Scan(&oldJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return workflow.ErrNotFound
		}
		return err
	}
	var old workflow.Run
	if err = json.Unmarshal([]byte(oldJSON), &old); err != nil {
		return err
	}
	if old.Status != r.Status {
		if err = appendEventTx(ctx, tx, r.ID, "run."+string(r.Status), "", "", map[string]any{"previous_status": old.Status, "error": r.Error}); err != nil {
			return err
		}
	}
	for nodeID, state := range r.Nodes {
		previous := old.Nodes[nodeID]
		if previous.Status != state.Status {
			eventType := "node." + string(state.Status)
			if err = appendEventTx(ctx, tx, r.ID, eventType, nodeID, state.WorkerID, map[string]any{"previous_status": previous.Status, "attempt": state.Attempt, "error": state.Error, "duration_ms": state.DurationMillis}); err != nil {
				return err
			}
		}
	}
	var result sql.Result
	if fenced {
		result, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET status=?,current_node=?,state_json=?,updated_at=? WHERE id=? AND EXISTS (SELECT 1 FROM workflow_jobs j WHERE j.run_id=? AND j.status='running' AND j.lease_owner=? AND j.fencing_token=? AND j.lease_until>?)`, r.Status, nullableString(r.CurrentNode), string(b), time.Now().Unix(), r.ID, r.ID, owner, fenceToken, time.Now().Unix())
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET status=?,current_node=?,state_json=?,updated_at=? WHERE id=?`, r.Status, nullableString(r.CurrentNode), string(b), time.Now().Unix(), r.ID)
	}
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return errors.New("workflow checkpoint rejected by fencing token")
	}
	return tx.Commit()
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func appendEventTx(ctx context.Context, tx *sql.Tx, runID, typ, nodeID, workerID string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var seq int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM workflow_events WHERE run_id=?`, runID).Scan(&seq); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workflow_events(run_id,sequence,event_type,node_id,worker_id,occurred_at,data_json) VALUES(?,?,?,?,?,?,?)`, runID, seq, typ, nullableString(nodeID), nullableString(workerID), time.Now().UnixMilli(), string(b))
	return err
}

func (s *Store) Events(ctx context.Context, runID string, after int64, limit int) ([]workflow.Event, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,run_id,sequence,event_type,node_id,worker_id,occurred_at,data_json FROM workflow_events WHERE run_id=? AND sequence>? ORDER BY sequence LIMIT ?`, runID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]workflow.Event, 0)
	for rows.Next() {
		var e workflow.Event
		var node, worker sql.NullString
		var ms int64
		var data string
		if err = rows.Scan(&e.ID, &e.RunID, &e.Sequence, &e.Type, &node, &worker, &ms, &data); err != nil {
			return nil, err
		}
		e.NodeID = node.String
		e.WorkerID = worker.String
		e.OccurredAt = time.UnixMilli(ms).UTC()
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) NodeObservations(ctx context.Context, runID string) ([]workflow.NodeObservation, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	definition, err := s.GetDefinition(ctx, run.WorkflowID, run.WorkflowVersion)
	if err != nil {
		return nil, err
	}
	out := make([]workflow.NodeObservation, 0, len(definition.Nodes))
	for _, node := range definition.Nodes {
		state := run.Nodes[node.ID]
		if state.Status == "" {
			state.Status = workflow.NodePending
		}
		var attempt int
		var owner sql.NullString
		_ = s.db.QueryRowContext(ctx, `SELECT attempt,owner FROM workflow_node_executions WHERE run_id=? AND node_id=?`, runID, node.ID).Scan(&attempt, &owner)
		if attempt < state.Attempt {
			attempt = state.Attempt
		}
		duration := state.DurationMillis
		if duration == 0 && !state.StartedAt.IsZero() {
			end := state.FinishedAt
			if end.IsZero() {
				end = time.Now().UTC()
			}
			duration = end.Sub(state.StartedAt).Milliseconds()
		}
		out = append(out, workflow.NodeObservation{NodeID: node.ID, Status: state.Status, Attempt: attempt, WorkerID: firstNonEmpty(state.WorkerID, owner.String), StartedAt: state.StartedAt, FinishedAt: state.FinishedAt, DurationMillis: duration, Error: state.Error})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// RecoverFailedNode performs an operator-approved recovery and requeues the run atomically.
func (s *Store) RecoverFailedNode(ctx context.Context, runID, nodeID, action, comment string, maxAttempts int) (workflow.Run, error) {
	var run workflow.Run
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return run, err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT state_json FROM workflow_runs WHERE id=?`, runID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return run, workflow.ErrNotFound
		}
		return run, err
	}
	if err = json.Unmarshal([]byte(raw), &run); err != nil {
		return run, err
	}
	if run.Status != workflow.RunFailed {
		return run, workflow.ErrInvalidTransition
	}
	state, ok := run.Nodes[nodeID]
	if !ok || (state.Status != workflow.NodeFailed && state.Status != workflow.NodeInterrupted) {
		return run, workflow.ErrRecoveryNotAllowed
	}
	if state.Status == workflow.NodeInterrupted && action == "retry" {
		return run, fmt.Errorf("%w: interrupted node has an indeterminate outcome; use skip or create a new run", workflow.ErrRecoveryNotAllowed)
	}
	var defJSON string
	if err = tx.QueryRowContext(ctx, `SELECT definition_json FROM workflows WHERE id=? AND version=?`, run.WorkflowID, run.WorkflowVersion).Scan(&defJSON); err != nil {
		return run, err
	}
	var def workflow.Definition
	if err = json.Unmarshal([]byte(defJSON), &def); err != nil {
		return run, err
	}
	var targetType workflow.NodeType
	for _, node := range def.Nodes {
		if node.ID == nodeID {
			targetType = node.Type
			break
		}
	}
	if targetType == "" {
		return run, workflow.ErrRecoveryNotAllowed
	}
	if action == "skip" && targetType == workflow.NodeCondition {
		return run, fmt.Errorf("%w: condition nodes cannot be skipped because the active branch is unknown", workflow.ErrRecoveryNotAllowed)
	}
	if action == "retry" {
		if state.Status != workflow.NodeFailed {
			return run, workflow.ErrRecoveryNotAllowed
		}
		state.Status = workflow.NodePending
		state.Error = ""
		state.Output = nil
		state.FinishedAt = time.Time{}
		state.StartedAt = time.Time{}
		state.WorkerID = ""
		state.DurationMillis = 0
		if _, err = tx.ExecContext(ctx, `DELETE FROM workflow_node_executions WHERE run_id=? AND node_id=? AND status='failed'`, runID, nodeID); err != nil {
			return run, err
		}
		run.PendingNodes = append(run.PendingNodes, nodeID)
	} else if action == "skip" {
		state.Status = workflow.NodeSkipped
		state.ManualSkip = true
		state.Error = "operator skipped node: " + comment
		state.FinishedAt = time.Now().UTC()
		for _, edge := range def.Edges {
			if edge.From == nodeID {
				run.PendingNodes = append(run.PendingNodes, edge.To)
			}
		}
	} else {
		return run, workflow.ErrRecoveryNotAllowed
	}
	run.Nodes[nodeID] = state
	run.PendingNodes = uniqueStrings(run.PendingNodes)
	// A parallel wave may have produced more than one failed node. Stage each
	// operator decision, but do not resume until every failed/interrupted node
	// has an explicit decision.
	remainingFailedNodes := make([]string, 0)
	for id, nodeState := range run.Nodes {
		if id != nodeID && (nodeState.Status == workflow.NodeFailed || nodeState.Status == workflow.NodeInterrupted) {
			remainingFailedNodes = append(remainingFailedNodes, id)
		}
	}
	sort.Strings(remainingFailedNodes)
	unresolved := len(remainingFailedNodes) > 0
	run.Error = ""
	run.CurrentNode = ""
	run.UpdatedAt = time.Now().UTC()
	if unresolved {
		run.Status = workflow.RunFailed
		run.Error = "operator recovery pending for other failed or interrupted nodes"
	} else {
		run.Status = workflow.RunQueued
		run.FinishedAt = time.Time{}
	}
	b, err := json.Marshal(run)
	if err != nil {
		return run, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET status=?,current_node=NULL,state_json=?,updated_at=? WHERE id=?`, run.Status, string(b), run.UpdatedAt.Unix(), runID); err != nil {
		return run, err
	}
	if err = appendEventTx(ctx, tx, runID, "node."+string(state.Status), nodeID, "", map[string]any{"operator_recovery": true, "action": action, "comment": comment}); err != nil {
		return run, err
	}
	if unresolved {
		if err = appendEventTx(ctx, tx, runID, "run.operator_recovery_staged", nodeID, "", map[string]any{"remaining_failed_nodes": remainingFailedNodes}); err != nil {
			return run, err
		}
	} else {
		if maxAttempts < 1 {
			maxAttempts = 3
		}
		res, updateErr := tx.ExecContext(ctx, `UPDATE workflow_jobs SET status='queued',attempts=0,max_attempts=?,available_at=?,lease_owner=NULL,lease_until=NULL,last_error=NULL,updated_at=? WHERE run_id=? AND status IN ('failed','interrupted','succeeded')`, maxAttempts, time.Now().Unix(), time.Now().Unix(), runID)
		if updateErr != nil {
			return run, updateErr
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return run, errors.New("workflow job is not recoverable")
		}
		if err = appendEventTx(ctx, tx, runID, "run.queued", "", "", map[string]any{"operator_recovery": true}); err != nil {
			return run, err
		}
	}
	if err = appendEventTx(ctx, tx, runID, "run.operator_recovery", nodeID, "", map[string]any{"action": action, "comment": comment}); err != nil {
		return run, err
	}
	if err = tx.Commit(); err != nil {
		return run, err
	}
	return run, nil
}
func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func (s *Store) GetRun(ctx context.Context, id string) (workflow.Run, error) {
	var b string
	var r workflow.Run
	e := s.db.QueryRowContext(ctx, `SELECT state_json FROM workflow_runs WHERE id=?`, id).Scan(&b)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return r, workflow.ErrNotFound
		}
		return r, e
	}
	e = json.Unmarshal([]byte(b), &r)
	return r, e
}
func (s *Store) ListRuns(ctx context.Context, workflowID string, limit int) ([]workflow.Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, e := s.db.QueryContext(ctx, `SELECT state_json FROM workflow_runs WHERE workflow_id=? ORDER BY created_at DESC LIMIT ?`, workflowID, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []workflow.Run{}
	for rows.Next() {
		var b string
		var r workflow.Run
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(b), &r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Job is a durable workflow execution lease.
type Job struct {
	RunID      string
	Owner      string
	Attempts   int
	LeaseUntil time.Time
	FenceToken int64
}

func (s *Store) EnqueueRun(ctx context.Context, runID string, maxAttempts int) error {
	if maxAttempts < 1 {
		maxAttempts = 3
	}
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO workflow_jobs(id,run_id,status,max_attempts,available_at,created_at,updated_at) VALUES(?,?,'queued',?,?,?,?) ON CONFLICT(run_id) DO NOTHING`, runID, runID, maxAttempts, now, now, now)
	return err
}

// RequeueRun is reserved for a persisted human approval continuation.
func (s *Store) RequeueRun(ctx context.Context, runID string, maxAttempts int) error {
	if maxAttempts < 1 {
		maxAttempts = 3
	}
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_jobs SET status='queued',attempts=0,max_attempts=?,available_at=?,lease_owner=NULL,lease_until=NULL,last_error=NULL,updated_at=? WHERE run_id=? AND status='succeeded'`, maxAttempts, now, now, runID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("workflow job is not in a resumable state")
	}
	return nil
}

// ClaimRun atomically leases one queued workflow. Expired leases are reconciled
// before another worker can claim the run.
func (s *Store) ClaimRun(ctx context.Context, owner string, lease time.Duration) (Job, error) {
	var out Job
	if owner == "" || lease < time.Second {
		return out, errors.New("invalid workflow lease")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	// Repair the narrow crash window between persisting a queued run and inserting
	// (or re-arming) its queue row, including approval continuation.
	_, err = tx.ExecContext(ctx, `INSERT INTO workflow_jobs(id,run_id,status,max_attempts,available_at,created_at,updated_at)
		SELECT r.id,r.id,'queued',3,?,?,? FROM workflow_runs r WHERE r.status='queued' AND 1
		ON CONFLICT(run_id) DO UPDATE SET status='queued',attempts=0,available_at=excluded.available_at,lease_owner=NULL,lease_until=NULL,last_error=NULL,updated_at=excluded.updated_at
		WHERE workflow_jobs.status='succeeded'`, now, now, now)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT run_id,attempts,max_attempts FROM workflow_jobs WHERE status='running' AND lease_until<=?`, now)
	if err != nil {
		return out, err
	}
	type expiredJob struct {
		id                    string
		attempts, maxAttempts int
	}
	var expired []expiredJob
	for rows.Next() {
		var item expiredJob
		if err = rows.Scan(&item.id, &item.attempts, &item.maxAttempts); err != nil {
			rows.Close()
			return out, err
		}
		expired = append(expired, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	for _, expiredItem := range expired {
		id := expiredItem.id
		if err = appendEventTx(ctx, tx, id, "worker.lease_expired", "", "", map[string]any{"attempt": expiredItem.attempts}); err != nil {
			return out, err
		}
		var stateJSON string
		if err = tx.QueryRowContext(ctx, `SELECT state_json FROM workflow_runs WHERE id=?`, id).Scan(&stateJSON); err != nil {
			return out, err
		}
		var run workflow.Run
		if err = json.Unmarshal([]byte(stateJSON), &run); err != nil {
			return out, err
		}
		if run.Context == nil {
			run.Context = map[string]any{}
		}
		if run.Nodes == nil {
			run.Nodes = map[string]workflow.NodeState{}
		}
		indeterminate := false
		hasRecoverableAgent := false
		hasRecoverablePure := false
		reconciledAny := false
		definitionJSON := ""
		_ = tx.QueryRowContext(ctx, `SELECT definition_json FROM workflows WHERE id=? AND version=?`, run.WorkflowID, run.WorkflowVersion).Scan(&definitionJSON)
		var definition workflow.Definition
		_ = json.Unmarshal([]byte(definitionJSON), &definition)
		nodeTypes := map[string]workflow.NodeType{}
		for _, n := range definition.Nodes {
			nodeTypes[n.ID] = n.Type
		}
		for nodeID, state := range run.Nodes {
			if state.Status != workflow.NodeRunning {
				continue
			}
			var nodeStatus string
			var output sql.NullString
			nodeErr := tx.QueryRowContext(ctx, `SELECT status,output_json FROM workflow_node_executions WHERE run_id=? AND node_id=?`, id, nodeID).Scan(&nodeStatus, &output)
			if nodeErr == nil && nodeStatus == "succeeded" && output.Valid {
				state.Status = workflow.NodeSucceeded
				reconciledAny = true
				state.Output = json.RawMessage(output.String)
				state.FinishedAt = time.Now().UTC()
				state.Error = ""
				run.Nodes[nodeID] = state
				if err = appendEventTx(ctx, tx, id, "node.succeeded", nodeID, "", map[string]any{"recovered_from_ledger": true}); err != nil {
					return out, err
				}
				if nodeTypes[nodeID] == workflow.NodeAgent {
					var result struct {
						Content string `json:"content"`
						Status  string `json:"status"`
						TaskID  string `json:"task_id"`
					}
					if json.Unmarshal(state.Output, &result) == nil {
						run.Context[nodeID] = map[string]any{"content": result.Content, "status": result.Status, "task_id": result.TaskID}
					}
				}
				for _, edge := range definition.Edges {
					if edge.From != nodeID {
						continue
					}
					active := true
					if nodeTypes[nodeID] == workflow.NodeCondition {
						var cond struct {
							Matched bool `json:"matched"`
						}
						_ = json.Unmarshal(state.Output, &cond)
						active = (cond.Matched && edge.Handle == "true") || (!cond.Matched && edge.Handle == "false")
					}
					if active {
						run.PendingNodes = append(run.PendingNodes, edge.To)
					}
				}
				if run.CurrentNode == nodeID {
					run.CurrentNode = ""
				}
			} else if nodeTypes[nodeID] == workflow.NodeAgent && ((nodeErr == nil && (nodeStatus == "running" || nodeStatus == "failed")) || errors.Is(nodeErr, sql.ErrNoRows)) {
				// Agent snapshots are reconciled by the Workflow Engine.
				hasRecoverableAgent = true
			} else if (nodeTypes[nodeID] == workflow.NodeStart || nodeTypes[nodeID] == workflow.NodeCondition || nodeTypes[nodeID] == workflow.NodeEnd) && ((nodeErr == nil && (nodeStatus == "running" || nodeStatus == "failed")) || errors.Is(nodeErr, sql.ErrNoRows)) {
				hasRecoverablePure = true
			} else {
				state.Status = workflow.NodeInterrupted
				state.Error = "worker lease expired while node was running; outcome is indeterminate"
				run.Nodes[nodeID] = state
				if err = appendEventTx(ctx, tx, id, "node.interrupted", nodeID, "", map[string]any{"reason": state.Error}); err != nil {
					return out, err
				}
				indeterminate = true
			}
		}
		allNodesResolved := len(definition.Nodes) > 0
		for _, node := range definition.Nodes {
			st := run.Nodes[node.ID].Status
			if st != workflow.NodeSucceeded && st != workflow.NodeSkipped {
				allNodesResolved = false
				break
			}
		}
		if !indeterminate && allNodesResolved {
			run.Status = workflow.RunSucceeded
			run.CurrentNode = ""
			run.PendingNodes = nil
			run.FinishedAt = time.Now().UTC()
			b, _ := json.Marshal(run)
			if _, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET status=?,current_node=NULL,state_json=?,updated_at=? WHERE id=?`, run.Status, string(b), now, id); err != nil {
				return out, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE workflow_jobs SET status='succeeded',lease_owner=NULL,lease_until=NULL,last_error=NULL,updated_at=? WHERE run_id=? AND status='running'`, now, id); err != nil {
				return out, err
			}
			if err = appendEventTx(ctx, tx, id, "run.succeeded", "", "", map[string]any{"recovered_after_lease_expiry": true}); err != nil {
				return out, err
			}
			continue
		}
		if indeterminate {
			run.Status = workflow.RunFailed
			run.Error = "workflow interrupted: one or more node outcomes are indeterminate; automatic replay was refused"
			run.FinishedAt = time.Now().UTC()
			b, _ := json.Marshal(run)
			if _, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET status=?,state_json=?,updated_at=? WHERE id=?`, run.Status, string(b), now, id); err != nil {
				return out, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE workflow_jobs SET status='interrupted',last_error=?,lease_owner=NULL,lease_until=NULL,updated_at=? WHERE run_id=?`, run.Error, now, id); err != nil {
				return out, err
			}
			if err = appendEventTx(ctx, tx, id, "run.failed", "", "", map[string]any{"reason": run.Error, "recovery": "manual_required"}); err != nil {
				return out, err
			}
		} else if run.Status == workflow.RunWaitingApproval || run.Status == workflow.RunSucceeded || run.Status == workflow.RunRejected || run.Status == workflow.RunCanceled {
			jobStatus := "succeeded"
			if run.Status == workflow.RunFailed {
				jobStatus = "failed"
			}
			if _, err = tx.ExecContext(ctx, `UPDATE workflow_jobs SET status=?,lease_owner=NULL,lease_until=NULL,last_error=NULL,updated_at=? WHERE run_id=? AND status='running'`, jobStatus, now, id); err != nil {
				return out, err
			}
		} else if run.Status == workflow.RunFailed {
			if _, err = tx.ExecContext(ctx, `UPDATE workflow_jobs SET status='failed',lease_owner=NULL,lease_until=NULL,last_error=?,updated_at=? WHERE run_id=? AND status='running'`, run.Error, now, id); err != nil {
				return out, err
			}
		} else if expiredItem.attempts >= expiredItem.maxAttempts && !hasRecoverableAgent && !hasRecoverablePure && !reconciledAny {
			run.Status = workflow.RunFailed
			run.Error = "workflow worker lease expired and retry limit was reached"
			run.FinishedAt = time.Now().UTC()
			b, _ := json.Marshal(run)
			if _, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET status=?,state_json=?,updated_at=? WHERE id=?`, run.Status, string(b), now, id); err != nil {
				return out, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE workflow_jobs SET status='failed',last_error=?,lease_owner=NULL,lease_until=NULL,updated_at=? WHERE run_id=? AND status='running'`, run.Error, now, id); err != nil {
				return out, err
			}
			if err = appendEventTx(ctx, tx, id, "run.failed", "", "", map[string]any{"reason": run.Error}); err != nil {
				return out, err
			}
		} else {
			run.Status = workflow.RunQueued
			b, _ := json.Marshal(run)
			if _, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET status=?,state_json=?,updated_at=? WHERE id=?`, run.Status, string(b), now, id); err != nil {
				return out, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE workflow_jobs SET status='queued',lease_owner=NULL,lease_until=NULL,available_at=?,updated_at=? WHERE run_id=? AND status='running'`, now, now, id); err != nil {
				return out, err
			}
			if err = appendEventTx(ctx, tx, id, "run.queued", "", "", map[string]any{"recovered_after_lease_expiry": true}); err != nil {
				return out, err
			}
		}
	}
	var runID string
	err = tx.QueryRowContext(ctx, `SELECT run_id FROM workflow_jobs WHERE status='queued' AND available_at<=? ORDER BY created_at LIMIT 1`, now).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, sql.ErrNoRows
	}
	if err != nil {
		return out, err
	}
	until := time.Now().Add(lease)
	res, err := tx.ExecContext(ctx, `UPDATE workflow_jobs SET status='running',attempts=attempts+1,fencing_token=fencing_token+1,lease_owner=?,lease_until=?,updated_at=? WHERE run_id=? AND status='queued'`, owner, until.Unix(), now, runID)
	if err != nil {
		return out, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return out, sql.ErrNoRows
	}
	if err = tx.QueryRowContext(ctx, `SELECT attempts FROM workflow_jobs WHERE run_id=?`, runID).Scan(&out.Attempts); err != nil {
		return out, err
	}
	out.RunID, out.Owner, out.LeaseUntil = runID, owner, until
	if err = tx.QueryRowContext(ctx, `SELECT fencing_token FROM workflow_jobs WHERE run_id=?`, runID).Scan(&out.FenceToken); err != nil {
		return Job{}, err
	}
	if err = appendEventTx(ctx, tx, runID, "worker.claimed", "", owner, map[string]any{"attempt": out.Attempts, "lease_until": until.UTC(), "fencing_token": out.FenceToken}); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return out, nil
}

func (s *Store) HeartbeatRun(ctx context.Context, job Job, lease time.Duration) error {
	until := time.Now().Add(lease).Unix()
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_jobs SET lease_until=?,updated_at=? WHERE run_id=? AND status='running' AND lease_owner=? AND fencing_token=? AND lease_until>? `, until, time.Now().Unix(), job.RunID, job.Owner, job.FenceToken, time.Now().Unix())
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("workflow lease lost or fencing token superseded")
	}
	// Keep every currently executing node lease aligned with the owning run lease.
	_, err = s.db.ExecContext(ctx, `UPDATE workflow_node_executions SET lease_until=? WHERE run_id=? AND status='running' AND owner=? AND fencing_token=?`, until, job.RunID, job.Owner, job.FenceToken)
	return err
}

func (s *Store) FinishRun(ctx context.Context, job Job, run workflow.Run, runErr error) error {
	status := "succeeded"
	if run.Status == workflow.RunFailed || runErr != nil {
		status = "failed"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE workflow_jobs SET status=?,last_error=?,lease_owner=NULL,lease_until=NULL,updated_at=? WHERE run_id=? AND status='running' AND lease_owner=? AND fencing_token=? AND lease_until>?`, status, errorString(runErr), time.Now().Unix(), job.RunID, job.Owner, job.FenceToken, time.Now().Unix())
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("workflow lease lost before completion")
	}
	if err = appendEventTx(ctx, tx, job.RunID, "worker.finished", "", job.Owner, map[string]any{"job_status": status, "run_status": run.Status, "error": errorString(runErr)}); err != nil {
		return err
	}
	return tx.Commit()
}

func errorString(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

var ErrNodeIndeterminate = errors.New("workflow node execution is indeterminate; refusing automatic replay")

// BeginNode creates a durable idempotency record before side effects begin.
// A previous success is returned to the caller; a stale/running attempt is never replayed.
func (s *Store) BeginNode(ctx context.Context, runID, nodeID, key, owner string, fenceToken int64, lease time.Duration) (bool, json.RawMessage, error) {
	var status string
	var output sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT status,output_json FROM workflow_node_executions WHERE run_id=? AND node_id=?`, runID, nodeID).Scan(&status, &output)
	if err == nil {
		if status == "succeeded" {
			return true, json.RawMessage(output.String), nil
		}
		return false, nil, ErrNodeIndeterminate
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, nil, err
	}
	insertResult, err := s.db.ExecContext(ctx, `INSERT INTO workflow_node_executions(run_id,node_id,idempotency_key,status,owner,lease_until,attempt,started_at,fencing_token) SELECT ?,?,?, 'running',?,?,1,?,? WHERE ?=0 OR EXISTS (SELECT 1 FROM workflow_jobs j WHERE j.run_id=? AND j.status='running' AND j.lease_owner=? AND j.fencing_token=? AND j.lease_until>?)`, runID, nodeID, key, owner, time.Now().Add(lease).Unix(), time.Now().Unix(), fenceToken, fenceToken, runID, owner, fenceToken, time.Now().Unix())
	if err == nil {
		if n, _ := insertResult.RowsAffected(); n != 1 {
			return false, nil, errors.New("workflow node claim rejected by fencing token")
		}
	}
	if err != nil {
		// A concurrent insert is treated as an in-flight duplicate, never as permission to execute.
		var exists int
		if readErr := s.db.QueryRowContext(ctx, `SELECT 1 FROM workflow_node_executions WHERE run_id=? AND node_id=?`, runID, nodeID).Scan(&exists); readErr == nil {
			return false, nil, ErrNodeIndeterminate
		}
		return false, nil, err
	}
	return false, nil, nil
}

func (s *Store) CompleteNode(ctx context.Context, runID, nodeID, owner string, fenceToken int64, output json.RawMessage, runErr error) error {
	status := "succeeded"
	var errText any
	if runErr != nil {
		status = "failed"
		errText = runErr.Error()
	}
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_node_executions SET status=?,output_json=?,error_text=?,owner=NULL,lease_until=NULL,finished_at=? WHERE run_id=? AND node_id=? AND status='running' AND owner=? AND fencing_token=? AND (?=0 OR EXISTS (SELECT 1 FROM workflow_jobs j WHERE j.run_id=? AND j.status='running' AND j.lease_owner=? AND j.fencing_token=? AND j.lease_until>?))`, status, nullableJSON(output), errText, time.Now().Unix(), runID, nodeID, owner, fenceToken, fenceToken, runID, owner, fenceToken, time.Now().Unix())
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("workflow node execution lease lost")
	}
	return nil
}
func nullableJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func (s *Store) LookupNode(ctx context.Context, runID, nodeID string) (string, json.RawMessage, error) {
	var status string
	var output sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT status,output_json FROM workflow_node_executions WHERE run_id=? AND node_id=?`, runID, nodeID).Scan(&status, &output)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if output.Valid {
		return status, json.RawMessage(output.String), nil
	}
	return status, nil, nil
}
func (s *Store) RecoverNode(ctx context.Context, runID, nodeID string, output json.RawMessage) error {
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_node_executions SET status='succeeded',output_json=?,error_text=NULL,owner=NULL,lease_until=NULL,finished_at=? WHERE run_id=? AND node_id=? AND status IN ('running','failed')`, string(output), time.Now().Unix(), runID, nodeID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("workflow node recovery lease mismatch")
	}
	return nil
}
func (s *Store) ResetNode(ctx context.Context, runID, nodeID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM workflow_node_executions WHERE run_id=? AND node_id=? AND status IN ('running','failed')`, runID, nodeID)
	return err
}
