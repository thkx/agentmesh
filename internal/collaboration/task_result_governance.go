package collaboration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type TaskChildProgress struct {
	ParentTaskID string `json:"parent_task_id"`
	ParentAgentID string `json:"parent_agent_id,omitempty"`
	TotalChildren int `json:"total_children"`
	CompletedChildren int `json:"completed_children"`
	BlockedChildren int `json:"blocked_children"`
	ChildrenWithResults int `json:"children_with_results"`
	ChildrenFullyAccepted int `json:"children_fully_accepted"`
	NeedsReviewChildren int `json:"needs_review_children"`
	ReadyForParentReview bool `json:"ready_for_parent_review"`
}

func (s *Service) ResolveTaskResultAssessment(ctx context.Context, recipientAgentID, assignmentID, reviewerID, outcome, note string) (TaskResultAssessment, error) {
	recipientAgentID, assignmentID, reviewerID = strings.TrimSpace(recipientAgentID), strings.TrimSpace(assignmentID), strings.TrimSpace(reviewerID)
	outcome, note = strings.TrimSpace(outcome), strings.TrimSpace(note)
	if recipientAgentID == "" || assignmentID == "" || reviewerID == "" || (outcome != "accepted" && outcome != "rejected") || len(note)>4000 { return TaskResultAssessment{}, ErrInvalid }
	tx, err := s.db.BeginTx(ctx,nil); if err != nil { return TaskResultAssessment{},err }; defer tx.Rollback()
	item, err := scanTaskResultAssessment(tx.QueryRowContext(ctx,`SELECT id,result_id,workspace_id,task_id,assignment_id,recipient_agent_id,outcome,reason,confidence,evidence_json,created_at,COALESCE(reviewer_id,''),COALESCE(decision_note,''),reviewed_at FROM agent_task_result_assessments WHERE recipient_agent_id=? AND assignment_id=?`,recipientAgentID,assignmentID))
	if err != nil { return TaskResultAssessment{},err }
	now:=s.clock().UTC()
	if item.ReviewedAt!=nil { if item.Outcome!=outcome || item.ReviewerID!=reviewerID || item.DecisionNote!=note { return TaskResultAssessment{},ErrConflict }; return item,nil }
	if item.Outcome!="human_review" { return TaskResultAssessment{},ErrConflict }
	_,err=tx.ExecContext(ctx,`UPDATE agent_task_result_assessments SET outcome=?,reviewer_id=?,decision_note=?,reviewed_at=? WHERE id=? AND outcome='human_review' AND reviewed_at IS NULL`,outcome,reviewerID,note,now.UnixMilli(),item.ID); if err!=nil{return TaskResultAssessment{},err}
	auditID,err:=newID(); if err!=nil{return TaskResultAssessment{},err}
	_,err=tx.ExecContext(ctx,`INSERT INTO agent_task_result_review_audit(id,assessment_id,result_id,workspace_id,task_id,assignment_id,recipient_agent_id,reviewer_id,outcome,note,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,auditID,item.ID,item.ResultID,item.WorkspaceID,item.TaskID,item.AssignmentID,item.RecipientAgentID,reviewerID,outcome,note,now.UnixMilli()); if err!=nil{return TaskResultAssessment{},err}
	if outcome=="rejected" {
		_,err=tx.ExecContext(ctx,"UPDATE collaboration_tasks SET status='blocked',updated_at=? WHERE id=? AND status<>'canceled'",now.UnixMilli(),item.TaskID); if err!=nil{return TaskResultAssessment{},err}
		if err=s.writeEventTx(ctx,tx,item.WorkspaceID,"task",item.TaskID,"task.result_rejected",map[string]any{"task_id":item.TaskID,"assignment_id":item.AssignmentID,"recipient_agent_id":item.RecipientAgentID,"reviewer_id":reviewerID,"reason":note});err!=nil{return TaskResultAssessment{},err}
	}
	if err=tx.Commit();err!=nil{return TaskResultAssessment{},err}
	return s.GetTaskResultAssessment(ctx,recipientAgentID,assignmentID)
}

func scanTaskResultAssessment(row interface{Scan(...any)error})(TaskResultAssessment,error){
	var item TaskResultAssessment; var evidence string; var created int64; var reviewed sql.NullInt64
	err:=row.Scan(&item.ID,&item.ResultID,&item.WorkspaceID,&item.TaskID,&item.AssignmentID,&item.RecipientAgentID,&item.Outcome,&item.Reason,&item.Confidence,&evidence,&created,&item.ReviewerID,&item.DecisionNote,&reviewed)
	if errors.Is(err,sql.ErrNoRows){return item,ErrNotFound}; if err!=nil{return item,err}
	item.Evidence=[]byte(evidence); item.CreatedAt=time.UnixMilli(created).UTC(); if reviewed.Valid { v:=time.UnixMilli(reviewed.Int64).UTC(); item.ReviewedAt=&v }; return item,nil
}

func (s *Service) GetTaskChildProgress(ctx context.Context,parentTaskID string)(TaskChildProgress,error){
	if _,err:=s.GetTask(ctx,parentTaskID);err!=nil{return TaskChildProgress{},err}
	p:=TaskChildProgress{ParentTaskID:parentTaskID}
	err:=s.db.QueryRowContext(ctx,`SELECT COALESCE((SELECT agent_id FROM task_assignments WHERE task_id=? AND status IN ('pending','claimed','completed') ORDER BY CASE status WHEN 'claimed' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END,updated_at DESC LIMIT 1),'')`,parentTaskID).Scan(&p.ParentAgentID);if err!=nil{return p,err}
	if p.ParentAgentID=="" { return p,nil }
	err=s.db.QueryRowContext(ctx,`SELECT COUNT(*),
	COALESCE(SUM(CASE WHEN child.status='completed' THEN 1 ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN child.status='blocked' THEN 1 ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN EXISTS(SELECT 1 FROM agent_task_results r WHERE r.task_id=child.id AND r.recipient_agent_id=?) THEN 1 ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN child.status='completed' AND EXISTS(SELECT 1 FROM agent_task_results r JOIN agent_task_result_assessments a ON a.result_id=r.id WHERE r.task_id=child.id AND r.recipient_agent_id=? AND a.outcome='accepted') THEN 1 ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN EXISTS(SELECT 1 FROM agent_task_results r LEFT JOIN agent_task_result_assessments a ON a.result_id=r.id AND a.recipient_agent_id=r.recipient_agent_id WHERE r.task_id=child.id AND r.recipient_agent_id=? AND (a.id IS NULL OR a.outcome IN ('human_review','rejected'))) THEN 1 ELSE 0 END),0)
	FROM collaboration_tasks child WHERE child.parent_task_id=?`,p.ParentAgentID,p.ParentAgentID,p.ParentAgentID,parentTaskID).Scan(&p.TotalChildren,&p.CompletedChildren,&p.BlockedChildren,&p.ChildrenWithResults,&p.ChildrenFullyAccepted,&p.NeedsReviewChildren)
	if err!=nil{return TaskChildProgress{},err}
	p.ReadyForParentReview=p.TotalChildren>0&&p.CompletedChildren==p.TotalChildren&&p.ChildrenFullyAccepted==p.TotalChildren&&p.BlockedChildren==0&&p.NeedsReviewChildren==0
	return p,nil
}

func (s *Service) FailAssignment(ctx context.Context,id,owner string,version int64,reason string,retryable bool)error{
	id,owner,reason=strings.TrimSpace(id),strings.TrimSpace(owner),strings.TrimSpace(reason);if id==""||owner==""||version<1||len(reason)>4000{return ErrInvalid}
	now:=s.clock().UTC();status:="rejected";if retryable{status="expired"}
	tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return err};defer tx.Rollback()
	var taskID,workspaceID string;err=tx.QueryRowContext(ctx,"SELECT a.task_id,t.workspace_id FROM task_assignments a JOIN collaboration_tasks t ON t.id=a.task_id WHERE a.id=?",id).Scan(&taskID,&workspaceID)
	if errors.Is(err,sql.ErrNoRows){return ErrNotFound};if err!=nil{return err}
	res,err:=tx.ExecContext(ctx,`UPDATE task_assignments SET status=?,failure_reason=?,lease_owner='',lease_expires_at=NULL,updated_at=? WHERE id=? AND status='claimed' AND lease_owner=? AND claim_version=? AND lease_expires_at>=?`,status,reason,now.UnixMilli(),id,owner,version,now.UnixMilli());if err!=nil{return err}
	n,err:=res.RowsAffected();if err!=nil{return err};if n!=1{return ErrLeaseLost}
	if _,err=tx.ExecContext(ctx,"UPDATE collaboration_tasks SET status='blocked',updated_at=? WHERE id=?",now.UnixMilli(),taskID);err!=nil{return err}
	if err=s.writeEventTx(ctx,tx,workspaceID,"task",taskID,"task.assignment_failed",map[string]any{"task_id":taskID,"assignment_id":id,"status":status,"retryable":retryable,"reason":reason});err!=nil{return err}
	return tx.Commit()
}

func (s *Service) RetryAssignment(ctx context.Context,id,requestedBy string)(Assignment,error){
	id,requestedBy=strings.TrimSpace(id),strings.TrimSpace(requestedBy);if id==""||requestedBy==""{return Assignment{},ErrInvalid}
	now:=s.clock().UTC();tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return Assignment{},err};defer tx.Rollback()
	var taskID,agentID,status,workspaceID string;var retries,maxRetries int
	err=tx.QueryRowContext(ctx,`SELECT a.task_id,a.agent_id,a.status,t.workspace_id,a.retry_count,a.max_retries FROM task_assignments a JOIN collaboration_tasks t ON t.id=a.task_id WHERE a.id=?`,id).Scan(&taskID,&agentID,&status,&workspaceID,&retries,&maxRetries)
	if errors.Is(err,sql.ErrNoRows){return Assignment{},ErrNotFound};if err!=nil{return Assignment{},err}
	if (status!="expired"&&status!="rejected")||retries>=maxRetries{return Assignment{},ErrConflict}
	res,err:=tx.ExecContext(ctx,`UPDATE task_assignments SET status='pending',retry_count=retry_count+1,assigned_by=?,updated_at=? WHERE id=? AND status IN ('expired','rejected') AND retry_count=?`,requestedBy,now.UnixMilli(),id,retries);if err!=nil{return Assignment{},err}
	n,err:=res.RowsAffected();if err!=nil{return Assignment{},err};if n!=1{return Assignment{},ErrConflict}
	if _,err=tx.ExecContext(ctx,"UPDATE collaboration_tasks SET status='assigned',updated_at=? WHERE id=? AND status IN ('blocked','assigned')",now.UnixMilli(),taskID);err!=nil{return Assignment{},err}
	if err=s.writeEventTx(ctx,tx,workspaceID,"task",taskID,"task.assignment_retried",map[string]any{"task_id":taskID,"assignment_id":id,"agent_id":agentID,"requested_by":requestedBy,"retry_count":retries+1});err!=nil{return Assignment{},err}
	if err=tx.Commit();err!=nil{return Assignment{},err};return s.findAssignment(ctx,taskID,agentID)
}

type TaskResultReviewAudit struct {
	ID string `json:"id"`; AssessmentID string `json:"assessment_id"`; ResultID string `json:"result_id"`; WorkspaceID string `json:"workspace_id"`; TaskID string `json:"task_id"`; AssignmentID string `json:"assignment_id"`; RecipientAgentID string `json:"recipient_agent_id"`; ReviewerID string `json:"reviewer_id"`; Outcome string `json:"outcome"`; Note string `json:"note"`; CreatedAt time.Time `json:"created_at"`
}
func(s *Service)ListTaskResultReviewAudit(ctx context.Context,recipientID,assignmentID string)([]TaskResultReviewAudit,error){
	if _,err:=s.GetTaskResultAssessment(ctx,recipientID,assignmentID);err!=nil{return nil,err}
	rows,err:=s.db.QueryContext(ctx,`SELECT id,assessment_id,result_id,workspace_id,task_id,assignment_id,recipient_agent_id,reviewer_id,outcome,note,created_at FROM agent_task_result_review_audit WHERE recipient_agent_id=? AND assignment_id=? ORDER BY created_at,id`,recipientID,assignmentID);if err!=nil{return nil,err};defer rows.Close()
	items:=make([]TaskResultReviewAudit,0);for rows.Next(){var v TaskResultReviewAudit;var created int64;if err=rows.Scan(&v.ID,&v.AssessmentID,&v.ResultID,&v.WorkspaceID,&v.TaskID,&v.AssignmentID,&v.RecipientAgentID,&v.ReviewerID,&v.Outcome,&v.Note,&created);err!=nil{return nil,err};v.CreatedAt=time.UnixMilli(created).UTC();items=append(items,v)};return items,rows.Err()
}
