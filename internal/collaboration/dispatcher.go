package collaboration

import (
 "context"
 "database/sql"
 "encoding/json"
 "errors"
 "time"
)

// Dispatcher fans persisted domain events into per-Agent inboxes. It never calls an LLM.
type Dispatcher struct { db *sql.DB; interval time.Duration }
func NewDispatcher(db *sql.DB, interval time.Duration)(*Dispatcher,error){if db==nil{return nil,errors.New("collaboration dispatcher database is nil")};if interval<=0{interval=time.Second};return &Dispatcher{db:db,interval:interval},nil}
func(d *Dispatcher) Run(ctx context.Context)error{ticker:=time.NewTicker(d.interval);defer ticker.Stop();for{_ = d.DispatchOnce(ctx,100);select{case<-ctx.Done():return ctx.Err();case<-ticker.C:}}}
func(d *Dispatcher) DispatchOnce(ctx context.Context,limit int)error{
 if limit<1||limit>500{limit=100}
 rows,err:=d.db.QueryContext(ctx,"SELECT id FROM collaboration_outbox WHERE published_at IS NULL ORDER BY created_at,id LIMIT ?",limit);if err!=nil{return err}
 ids:=[]string{};for rows.Next(){var id string;if err=rows.Scan(&id);err!=nil{rows.Close();return err};ids=append(ids,id)};if err=rows.Err();err!=nil{rows.Close();return err};rows.Close()
 for _,id:=range ids{if err=d.dispatchOne(ctx,id);err!=nil{return err}}
 return nil
}
func(d *Dispatcher) dispatchOne(ctx context.Context,id string)error{
 tx,err:=d.db.BeginTx(ctx,nil);if err!=nil{return err};defer tx.Rollback()
 var e Event;var payload string;var created int64
 err=tx.QueryRowContext(ctx,"SELECT id,workspace_id,aggregate_type,aggregate_id,event_type,payload_json,created_at FROM collaboration_outbox WHERE id=? AND published_at IS NULL",id).Scan(&e.ID,&e.WorkspaceID,&e.AggregateType,&e.AggregateID,&e.Type,&payload,&created)
 if errors.Is(err,sql.ErrNoRows){return nil};if err!=nil{return err};e.Payload=json.RawMessage(payload);e.CreatedAt=time.UnixMilli(created).UTC()
 recipients:=[]string{}
 switch e.Type{
 case "task.assigned":
  var p struct{AgentID string `json:"agent_id"`};if err=json.Unmarshal(e.Payload,&p);err!=nil{return err};if p.AgentID!=""{recipients=append(recipients,p.AgentID)}
 case "message.created":
  var p struct{ChannelID string `json:"channel_id"`};if err=json.Unmarshal(e.Payload,&p);err!=nil{return err}
  rs,qerr:=tx.QueryContext(ctx,"SELECT agent_id FROM collaboration_channel_members WHERE channel_id=?",p.ChannelID);if qerr!=nil{return qerr};for rs.Next(){var aid string;if qerr=rs.Scan(&aid);qerr!=nil{rs.Close();return qerr};recipients=append(recipients,aid)};if qerr=rs.Err();qerr!=nil{rs.Close();return qerr};rs.Close()
 default:
  rs,qerr:=tx.QueryContext(ctx,"SELECT id FROM agent_identities WHERE workspace_id=? AND status<>'disabled'",e.WorkspaceID);if qerr!=nil{return qerr};for rs.Next(){var aid string;if qerr=rs.Scan(&aid);qerr!=nil{rs.Close();return qerr};recipients=append(recipients,aid)};if qerr=rs.Err();qerr!=nil{rs.Close();return qerr};rs.Close()
 }
 now:=time.Now().UTC().UnixMilli()
 for _,aid:=range recipients{_,err=tx.ExecContext(ctx,"INSERT OR IGNORE INTO collaboration_inbox(id,agent_id,event_id,event_type,payload_json,status,received_at) VALUES(?,?,?,?,?,'pending',?)",e.ID+":"+aid,aid,e.ID,e.Type,payload,now);if err!=nil{return err}}
 _,err=tx.ExecContext(ctx,"UPDATE collaboration_outbox SET published_at=?,attempts=attempts+1,last_error='' WHERE id=? AND published_at IS NULL",now,id);if err!=nil{return err}
 return tx.Commit()
}
