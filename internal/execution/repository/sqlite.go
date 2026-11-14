package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/execution"
)

type SQLiteStore struct { db *sql.DB }

func NewSQLiteStore(db *sql.DB) (*SQLiteStore,error) {
	if db==nil { return nil, errors.New("execution database is nil") }
	return &SQLiteStore{db:db},nil
}

const entityColumns = `id,kind,runtime,COALESCE(runtime_ref,''),COALESCE(task_id,''),status,input_json,result_json,error_text,COALESCE(idempotency_key,''),created_at,updated_at,started_at,finished_at`

func (s *SQLiteStore) Create(ctx context.Context,e dom.Entity)(dom.Entity,error) {
	if err:=e.Validate(); err!=nil { return dom.Entity{},err }
	now:=time.Now().UTC()
	if e.CreatedAt.IsZero(){e.CreatedAt=now}; if e.UpdatedAt.IsZero(){e.UpdatedAt=now}
	_,err:=s.db.ExecContext(ctx,`INSERT INTO executions(id,kind,runtime,runtime_ref,task_id,status,input_json,result_json,error_text,idempotency_key,created_at,updated_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID,e.Kind,e.Runtime,nullString(e.RuntimeRef),nullString(e.TaskID),e.Status,nullableJSON(e.Input),nullableJSON(e.Result),e.Error,nullString(e.IdempotencyKey),e.CreatedAt.UnixNano(),e.UpdatedAt.UnixNano(),nullableTime(e.StartedAt),nullableTime(e.FinishedAt))
	if err!=nil {
		if e.IdempotencyKey!="" {
			old,lookup:=s.GetByIdempotencyKey(ctx,e.IdempotencyKey)
			if lookup==nil {
				if old.Kind==e.Kind && old.Runtime==e.Runtime && old.TaskID==e.TaskID && string(old.Input)==string(e.Input) { return old,nil }
				return dom.Entity{},dom.ErrConflict
			}
		}
		if _,lookup:=s.Get(ctx,e.ID); lookup==nil { return dom.Entity{},dom.ErrConflict }
		return dom.Entity{},fmt.Errorf("insert execution: %w",err)
	}
	return s.Get(ctx,e.ID)
}

func (s *SQLiteStore) Get(ctx context.Context,id string)(dom.Entity,error) {
	e,err:=scanEntity(s.db.QueryRowContext(ctx,`SELECT `+entityColumns+` FROM executions WHERE id=?`,id))
	if errors.Is(err,sql.ErrNoRows){return dom.Entity{},dom.ErrNotFound}; return e,err
}

func (s *SQLiteStore) GetByIdempotencyKey(ctx context.Context,key string)(dom.Entity,error) {
	e,err:=scanEntity(s.db.QueryRowContext(ctx,`SELECT `+entityColumns+` FROM executions WHERE idempotency_key=?`,key))
	if errors.Is(err,sql.ErrNoRows){return dom.Entity{},dom.ErrNotFound}; return e,err
}

func (s *SQLiteStore) List(ctx context.Context,f dom.ListFilter)([]dom.Entity,error) {
	if f.Limit<=0 || f.Limit>200 { f.Limit=50 }; if f.Offset<0 { f.Offset=0 }
	query:=`SELECT `+entityColumns+` FROM executions WHERE (?='' OR status=?) AND (?='' OR kind=?) AND (?='' OR runtime=?) AND (?='' OR task_id=?) ORDER BY created_at DESC,id LIMIT ? OFFSET ?`
	rows,err:=s.db.QueryContext(ctx,query,f.Status,f.Status,f.Kind,f.Kind,f.Runtime,f.Runtime,f.TaskID,f.TaskID,f.Limit,f.Offset); if err!=nil{return nil,err}; defer rows.Close()
	out:=make([]dom.Entity,0)
	for rows.Next(){e,scanErr:=scanEntity(rows); if scanErr!=nil{return nil,scanErr}; out=append(out,e)}
	return out,rows.Err()
}

func (s *SQLiteStore) BindRuntimeRef(ctx context.Context,id,ref string)(dom.Entity,error) {
	if strings.TrimSpace(id)=="" || strings.TrimSpace(ref)=="" || len(ref)>256 { return dom.Entity{},dom.ErrInvalidEntity }
	res,err:=s.db.ExecContext(ctx,`UPDATE executions SET runtime_ref=?,updated_at=? WHERE id=? AND status IN ('queued','running') AND (runtime_ref IS NULL OR runtime_ref=?)`,ref,time.Now().UTC().UnixNano(),id,ref)
	if err!=nil{return dom.Entity{},err}; n,_:=res.RowsAffected()
	if n!=1 { if _,e:=s.Get(ctx,id); errors.Is(e,dom.ErrNotFound){return dom.Entity{},dom.ErrNotFound}; return dom.Entity{},dom.ErrConflict }
	return s.Get(ctx,id)
}

// Transition uses an expected-state predicate so concurrent workers cannot overwrite one another.
func (s *SQLiteStore) Transition(ctx context.Context,id string,from []dom.LifecycleStatus,to dom.LifecycleStatus,message string,result []byte)(dom.Entity,error) {
	if len(from)==0 || strings.TrimSpace(id)=="" || len(message)>4096 || len(result)>4<<20 || (len(result)>0 && !json.Valid(result)) { return dom.Entity{},dom.ErrInvalidEntity }
	for _,v:=range from { if !dom.CanTransition(v,to) { return dom.Entity{},dom.ErrInvalidTransition } }
	now:=time.Now().UTC()
	marks:=make([]string,len(from)); for i:=range from {marks[i]="?"}
	q:=`UPDATE executions SET status=?,error_text=?,result_json=?,updated_at=?,started_at=CASE WHEN ? IN ('running','succeeded','failed','canceled','timed_out','interrupted','needs_reconciliation') AND started_at IS NULL THEN ? ELSE started_at END,finished_at=CASE WHEN ? IN ('succeeded','failed','canceled','timed_out','interrupted','needs_reconciliation') THEN ? ELSE finished_at END WHERE id=? AND status IN (`+strings.Join(marks,",")+`) `
	args:=[]any{to,message,nullableJSON(result),now.UnixNano(),to,now.UnixNano(),to,now.UnixNano(),id}
	for _,v:=range from {args=append(args,v)}
	res,err:=s.db.ExecContext(ctx,q,args...); if err!=nil{return dom.Entity{},err}; n,_:=res.RowsAffected()
	if n!=1 { if _,e:=s.Get(ctx,id); errors.Is(e,dom.ErrNotFound){return dom.Entity{},dom.ErrNotFound}; return dom.Entity{},dom.ErrInvalidTransition }
	return s.Get(ctx,id)
}

func (s *SQLiteStore) CreateAttempt(ctx context.Context,a dom.Attempt)(dom.Attempt,error) {
	if err:=a.Validate();err!=nil{return dom.Attempt{},err}
	now:=time.Now().UTC(); if a.CreatedAt.IsZero(){a.CreatedAt=now}; if a.UpdatedAt.IsZero(){a.UpdatedAt=now}
	_,err:=s.db.ExecContext(ctx,`INSERT INTO execution_attempts(id,execution_id,attempt_number,runtime,runtime_ref,status,input_json,result_json,error_text,started_at,finished_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,a.ID,a.ExecutionID,a.Number,a.Runtime,nullString(a.RuntimeRef),a.Status,nullableJSON(a.Input),nullableJSON(a.Result),a.Error,a.StartedAt.UnixNano(),nullableTime(a.FinishedAt),a.CreatedAt.UnixNano(),a.UpdatedAt.UnixNano())
	if err!=nil{return dom.Attempt{},fmt.Errorf("insert execution attempt: %w",err)}; return s.GetAttempt(ctx,a.ID)
}

func (s *SQLiteStore) GetAttempt(ctx context.Context,id string)(dom.Attempt,error) {
	return scanAttempt(s.db.QueryRowContext(ctx,`SELECT id,execution_id,attempt_number,runtime,runtime_ref,status,input_json,result_json,error_text,started_at,finished_at,created_at,updated_at FROM execution_attempts WHERE id=?`,id))
}

func (s *SQLiteStore) GetAttemptByNumber(ctx context.Context, executionID string, number int) (dom.Attempt,error) {
	return scanAttempt(s.db.QueryRowContext(ctx,`SELECT id,execution_id,attempt_number,runtime,runtime_ref,status,input_json,result_json,error_text,started_at,finished_at,created_at,updated_at FROM execution_attempts WHERE execution_id=? AND attempt_number=?`,executionID,number))
}

func (s *SQLiteStore) ListAttempts(ctx context.Context, executionID string) ([]dom.Attempt,error) {
	rows,err:=s.db.QueryContext(ctx,`SELECT id,execution_id,attempt_number,runtime,runtime_ref,status,input_json,result_json,error_text,started_at,finished_at,created_at,updated_at FROM execution_attempts WHERE execution_id=? ORDER BY attempt_number`,executionID)
	if err!=nil{return nil,err}
	defer rows.Close()
	out:=make([]dom.Attempt,0)
	for rows.Next(){
		item,scanErr:=scanAttempt(rows)
		if scanErr!=nil{return nil,scanErr}
		out=append(out,item)
	}
	return out,rows.Err()
}

func (s *SQLiteStore) FinishAttempt(ctx context.Context,id string,status dom.AttemptStatus,ref string,result []byte,message string)(dom.Attempt,error) {
	switch status {case dom.AttemptSucceeded,dom.AttemptFailed,dom.AttemptCanceled,dom.AttemptTimedOut,dom.AttemptUnknown:default:return dom.Attempt{},dom.ErrInvalidAttempt}
	if len(result)>4<<20 || (len(result)>0&&!json.Valid(result)) || len(message)>4096 || len(ref)>256{return dom.Attempt{},dom.ErrInvalidAttempt}
	now:=time.Now().UTC();res,err:=s.db.ExecContext(ctx,`UPDATE execution_attempts SET status=?,runtime_ref=COALESCE(NULLIF(?,''),runtime_ref),result_json=?,error_text=?,finished_at=?,updated_at=? WHERE id=? AND status='running'`,status,ref,nullableJSON(result),message,now.UnixNano(),now.UnixNano(),id)
	if err!=nil{return dom.Attempt{},err};n,_:=res.RowsAffected();if n!=1{return dom.Attempt{},dom.ErrInvalidTransition};return s.GetAttempt(ctx,id)
}

type scanner interface{ Scan(...any) error }
func scanAttempt(row scanner)(dom.Attempt,error) {
	var a dom.Attempt
	var runtime,status string
	var ref,input,result sql.NullString
	var started,created,updated int64
	var finished sql.NullInt64
	err:=row.Scan(&a.ID,&a.ExecutionID,&a.Number,&runtime,&ref,&status,&input,&result,&a.Error,&started,&finished,&created,&updated)
	if errors.Is(err,sql.ErrNoRows){return dom.Attempt{},dom.ErrNotFound}
	if err!=nil{return dom.Attempt{},err}
	a.Runtime=dom.Runtime(runtime);a.Status=dom.AttemptStatus(status)
	a.StartedAt=time.Unix(0,started).UTC();a.CreatedAt=time.Unix(0,created).UTC();a.UpdatedAt=time.Unix(0,updated).UTC()
	if ref.Valid{a.RuntimeRef=ref.String};if input.Valid{a.Input=json.RawMessage(input.String)};if result.Valid{a.Result=json.RawMessage(result.String)}
	if finished.Valid{v:=time.Unix(0,finished.Int64).UTC();a.FinishedAt=&v}
	return a,nil
}
func scanEntity(row scanner)(dom.Entity,error) {
	var e dom.Entity;var kind,runtime,status string;var ref,task,input,result,key sql.NullString;var created,updated int64;var started,finished sql.NullInt64
	err:=row.Scan(&e.ID,&kind,&runtime,&ref,&task,&status,&input,&result,&e.Error,&key,&created,&updated,&started,&finished);if err!=nil{return dom.Entity{},err}
	e.Kind=dom.Kind(kind);e.Runtime=dom.Runtime(runtime);e.Status=dom.LifecycleStatus(status);e.CreatedAt=time.Unix(0,created).UTC();e.UpdatedAt=time.Unix(0,updated).UTC()
	if ref.Valid{e.RuntimeRef=ref.String};if task.Valid{e.TaskID=task.String};if input.Valid{e.Input=json.RawMessage(input.String)};if result.Valid{e.Result=json.RawMessage(result.String)};if key.Valid{e.IdempotencyKey=key.String}
	if started.Valid{v:=time.Unix(0,started.Int64).UTC();e.StartedAt=&v};if finished.Valid{v:=time.Unix(0,finished.Int64).UTC();e.FinishedAt=&v};return e,nil
}
func nullableJSON(v []byte) any {if len(v)==0{return nil};return string(v)}
func nullString(v string) any {if v==""{return nil};return v}
func nullableTime(v *time.Time) any {if v==nil{return nil};return v.UnixNano()}
