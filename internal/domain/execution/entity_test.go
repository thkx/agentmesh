package execution

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEntityValidate(t *testing.T) {
	e:=Entity{ID:"exec-1",Kind:KindAgent,Runtime:RuntimeAgent,Status:StatusQueued,Input:json.RawMessage(`{"prompt":"hello"}`)}
	if err:=e.Validate();err!=nil{t.Fatalf("valid entity: %v",err)}
	e.Input=json.RawMessage(`{broken`)
	if e.Validate()==nil{t.Fatal("expected invalid JSON rejection")}
}

func TestCanTransition(t *testing.T) {
	cases:=[]struct{from,to LifecycleStatus;want bool}{
		{StatusQueued,StatusRunning,true},
		{StatusRunning,StatusQueued,true},
		{StatusRunning,StatusSucceeded,true},
		{StatusSucceeded,StatusRunning,false},
		{StatusInterrupted,StatusSucceeded,true},
		{StatusNeedsReconciliation,StatusFailed,true},
		{StatusTimedOut,StatusQueued,false},
	}
	for _,tc:=range cases{if got:=CanTransition(tc.from,tc.to);got!=tc.want{t.Errorf("CanTransition(%s,%s)=%v want %v",tc.from,tc.to,got,tc.want)}}
}

func TestAttemptValidate(t *testing.T) {
	now:=time.Now().UTC()
	a:=Attempt{ID:"att-1",ExecutionID:"exec-1",Number:1,Runtime:RuntimeAgent,Status:AttemptRunning,StartedAt:now}
	if err:=a.Validate();err!=nil{t.Fatalf("valid attempt: %v",err)}
	a.Number=0
	if a.Validate()==nil{t.Fatal("expected invalid attempt number")}
}
