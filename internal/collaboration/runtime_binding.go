package collaboration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
)

type RuntimeBinding struct {
	AgentID            string         `json:"agent_id"`
	DefinitionID       string         `json:"definition_id"`
	DefinitionVersion  int            `json:"definition_version"`
	ModelID            string         `json:"model_id"`
	RuntimeWorkspaceID string         `json:"runtime_workspace_id"`
	Enabled            bool           `json:"enabled"`
	Definition         dom.Definition `json:"definition"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type AgentSession struct {
	ID                 string    `json:"id"`
	AgentID            string    `json:"agent_id"`
	Status             string    `json:"status"`
	CurrentExecutionID string    `json:"current_execution_id,omitempty"`
	LastExecutionID    string    `json:"last_execution_id,omitempty"`
	LastTaskID         string    `json:"last_task_id,omitempty"`
	ExecutionCount     int       `json:"execution_count"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (s *Service) BindRuntime(ctx context.Context, agentID, runtimeWorkspaceID string, definition dom.Definition) (RuntimeBinding, error) {
	var out RuntimeBinding
	runtimeWorkspaceID = strings.TrimSpace(runtimeWorkspaceID)
	if strings.TrimSpace(agentID) == "" || runtimeWorkspaceID == "" || definition.Validate() != nil || !definition.Enabled {
		return out, ErrInvalid
	}
	snapshot, err := json.Marshal(definition)
	if err != nil {
		return out, err
	}
	now := s.clock().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()

	var workspaceID string
	if err = tx.QueryRowContext(ctx, "SELECT workspace_id FROM agent_identities WHERE id=? AND status<>'disabled'", agentID).Scan(&workspaceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return out, ErrNotFound }
		return out, err
	}
	var version int
	var modelID string
	var enabled int
	if err = tx.QueryRowContext(ctx, "SELECT version,model_id,enabled FROM agent_definitions WHERE id=?", definition.ID).Scan(&version, &modelID, &enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return out, ErrNotFound }
		return out, err
	}
	if version != definition.Version || modelID != definition.ModelID || enabled != 1 {
		return out, ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_runtime_bindings
		(agent_id,definition_id,definition_version,runtime_workspace_id,definition_snapshot_json,enabled,created_at,updated_at)
		VALUES(?,?,?,?,?,1,?,?)
		ON CONFLICT(agent_id) DO UPDATE SET definition_id=excluded.definition_id,
		definition_version=excluded.definition_version,runtime_workspace_id=excluded.runtime_workspace_id,
		definition_snapshot_json=excluded.definition_snapshot_json,enabled=1,updated_at=excluded.updated_at`,
		agentID, definition.ID, definition.Version, runtimeWorkspaceID, string(snapshot), now.UnixMilli(), now.UnixMilli())
	if err != nil { return out, err }
	if _, err = tx.ExecContext(ctx, "UPDATE agent_identities SET definition_id=?,updated_at=? WHERE id=?", definition.ID, now.UnixMilli(), agentID); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil { return out, err }
	return RuntimeBinding{AgentID:agentID,DefinitionID:definition.ID,DefinitionVersion:definition.Version,ModelID:definition.ModelID,RuntimeWorkspaceID:runtimeWorkspaceID,Enabled:true,Definition:definition,CreatedAt:now,UpdatedAt:now}, nil
}

func (s *Service) GetRuntimeBinding(ctx context.Context, agentID string) (RuntimeBinding, error) {
	var out RuntimeBinding
	var snapshot string
	var enabled int
	var created, updated int64
	err := s.db.QueryRowContext(ctx, `SELECT b.agent_id,b.definition_id,b.definition_version,d.model_id,
		b.runtime_workspace_id,b.definition_snapshot_json,b.enabled,b.created_at,b.updated_at
		FROM agent_runtime_bindings b JOIN agent_definitions d ON d.id=b.definition_id
		WHERE b.agent_id=?`, agentID).Scan(&out.AgentID,&out.DefinitionID,&out.DefinitionVersion,&out.ModelID,&out.RuntimeWorkspaceID,&snapshot,&enabled,&created,&updated)
	if errors.Is(err,sql.ErrNoRows) { return out,ErrNotFound }
	if err != nil { return out,err }
	if err=json.Unmarshal([]byte(snapshot),&out.Definition);err!=nil{return out,err}
	out.Enabled=enabled==1
	out.CreatedAt=time.UnixMilli(created).UTC()
	out.UpdatedAt=time.UnixMilli(updated).UTC()
	return out,nil
}

func (s *Service) EnsureAgentSession(ctx context.Context, agentID string) (AgentSession,error) {
	var out AgentSession
	if _,err:=s.GetAgent(ctx,agentID);err!=nil{return out,err}
	now:=s.clock().UTC()
	id,err:=newID();if err!=nil{return out,err}
	_,err=s.db.ExecContext(ctx,`INSERT OR IGNORE INTO agent_sessions(id,agent_id,status,created_at,updated_at)
		VALUES(?,?,'active',?,?)`,id,agentID,now.UnixMilli(),now.UnixMilli())
	if err!=nil{return out,err}
	return s.GetActiveAgentSession(ctx,agentID)
}

func (s *Service) GetActiveAgentSession(ctx context.Context, agentID string) (AgentSession,error) {
	var v AgentSession
	var current,last,task sql.NullString
	var created,updated int64
	err:=s.db.QueryRowContext(ctx,`SELECT id,agent_id,status,current_execution_id,last_execution_id,last_task_id,execution_count,created_at,updated_at
		FROM agent_sessions WHERE agent_id=? AND status='active' ORDER BY created_at LIMIT 1`,agentID).
		Scan(&v.ID,&v.AgentID,&v.Status,&current,&last,&task,&v.ExecutionCount,&created,&updated)
	if errors.Is(err,sql.ErrNoRows){return v,ErrNotFound}
	if err!=nil{return v,err}
	if current.Valid{v.CurrentExecutionID=current.String};if last.Valid{v.LastExecutionID=last.String};if task.Valid{v.LastTaskID=task.String}
	v.CreatedAt=time.UnixMilli(created).UTC();v.UpdatedAt=time.UnixMilli(updated).UTC()
	return v,nil
}

func (s *Service) RecordSessionExecution(ctx context.Context, sessionID, executionID, taskID string, terminal bool) error {
	now:=s.clock().UTC()
	if terminal {
		_,err:=s.db.ExecContext(ctx,`UPDATE agent_sessions SET current_execution_id=NULL,last_execution_id=?,last_task_id=?,
			execution_count=execution_count+CASE WHEN COALESCE(last_execution_id,'')<>? THEN 1 ELSE 0 END,
			updated_at=? WHERE id=? AND status='active'`,executionID,taskID,executionID,now.UnixMilli(),sessionID)
		return err
	}
	_,err:=s.db.ExecContext(ctx,`UPDATE agent_sessions SET current_execution_id=?,last_task_id=?,updated_at=?
		WHERE id=? AND status='active'`,executionID,taskID,now.UnixMilli(),sessionID)
	return err
}

func (s *Service) FailAssignment(ctx context.Context, id, owner string, version int64, reason string) error {
	now:=s.clock().UTC()
	tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return err};defer tx.Rollback()
	var taskID,workspaceID string
	if err=tx.QueryRowContext(ctx,`SELECT a.task_id,t.workspace_id FROM task_assignments a JOIN collaboration_tasks t ON t.id=a.task_id WHERE a.id=?`,id).Scan(&taskID,&workspaceID);err!=nil{
		if errors.Is(err,sql.ErrNoRows){return ErrNotFound};return err
	}
	res,err:=tx.ExecContext(ctx,`UPDATE task_assignments SET status='rejected',lease_owner='',lease_expires_at=NULL,updated_at=?
		WHERE id=? AND status='claimed' AND lease_owner=? AND claim_version=? AND lease_expires_at>=?`,
		now.UnixMilli(),id,owner,version,now.UnixMilli())
	if err!=nil{return err};n,_:=res.RowsAffected();if n!=1{return ErrLeaseLost}
	if _,err=tx.ExecContext(ctx,"UPDATE collaboration_tasks SET status='blocked',updated_at=? WHERE id=?",now.UnixMilli(),taskID);err!=nil{return err}
	if err=s.writeEventTx(ctx,tx,workspaceID,"task",taskID,"task.execution_failed",map[string]any{"task_id":taskID,"assignment_id":id,"reason":reason});err!=nil{return err}
	return tx.Commit()
}
