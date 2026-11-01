package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	agentdom "github.com/thkx/agentmesh/internal/domain/agent"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/workflow/checkpoint"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeAgent struct{ calls int }

func (f *fakeAgent) Run(_ context.Context, t agentdom.Task) (agentdom.Result, error) {
	f.calls++
	return agentdom.Result{TaskID: t.ID, Status: agentdom.StatusSucceeded, Content: "review complete"}, nil
}

type memoryStore struct {
	mu          sync.Mutex
	defs        map[string]wf.Definition
	runs        map[string]wf.Run
	jobs        []string
	nodeRecords map[string]memoryNodeRecord
}
type memoryNodeRecord struct {
	status string
	output json.RawMessage
}

func newMemoryStore() *memoryStore {
	return &memoryStore{defs: map[string]wf.Definition{}, runs: map[string]wf.Run{}, nodeRecords: map[string]memoryNodeRecord{}}
}
func (s *memoryStore) SaveDefinition(_ context.Context, d wf.Definition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defs[d.ID] = d
	return nil
}
func (s *memoryStore) GetDefinition(_ context.Context, id string, v int) (wf.Definition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.defs[id]
	if !ok {
		return d, wf.ErrNotFound
	}
	return d, nil
}
func (s *memoryStore) CreateRun(_ context.Context, r wf.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[r.ID] = r
	return nil
}
func (s *memoryStore) SaveRun(_ context.Context, r wf.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[r.ID] = r
	return nil
}
func (s *memoryStore) GetRun(_ context.Context, id string) (wf.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return r, wf.ErrNotFound
	}
	return r, nil
}
func (s *memoryStore) ListRuns(context.Context, string, int) ([]wf.Run, error) { return nil, nil }
func (s *memoryStore) EnqueueRun(_ context.Context, id string, _ int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.jobs {
		if v == id {
			return nil
		}
	}
	s.jobs = append(s.jobs, id)
	return nil
}
func (s *memoryStore) RequeueRun(ctx context.Context, id string, max int) error {
	return s.EnqueueRun(ctx, id, max)
}
func (s *memoryStore) ClaimRun(_ context.Context, owner string, lease time.Duration) (checkpoint.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.jobs) == 0 {
		return checkpoint.Job{}, sql.ErrNoRows
	}
	id := s.jobs[0]
	s.jobs = s.jobs[1:]
	return checkpoint.Job{RunID: id, Owner: owner, LeaseUntil: time.Now().Add(lease)}, nil
}
func (s *memoryStore) HeartbeatRun(context.Context, checkpoint.Job, time.Duration) error { return nil }
func (s *memoryStore) FinishRun(context.Context, checkpoint.Job, wf.Run, error) error    { return nil }
func (s *memoryStore) BeginNode(_ context.Context, runID, nodeID, key, owner string, fenceToken int64, lease time.Duration) (bool, json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := runID + ":" + nodeID
	if v, ok := s.nodeRecords[k]; ok {
		if v.status == "succeeded" {
			return true, append(json.RawMessage(nil), v.output...), nil
		}
		return false, nil, checkpoint.ErrNodeIndeterminate
	}
	s.nodeRecords[k] = memoryNodeRecord{status: "running"}
	return false, nil, nil
}
func (s *memoryStore) CompleteNode(_ context.Context, runID, nodeID, owner string, fenceToken int64, out json.RawMessage, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.nodeRecords[runID+":"+nodeID]
	if runErr != nil {
		v.status = "failed"
	} else {
		v.status = "succeeded"
	}
	v.output = append(json.RawMessage(nil), out...)
	s.nodeRecords[runID+":"+nodeID] = v
	return nil
}
func (s *memoryStore) LookupNode(_ context.Context, runID, nodeID string) (string, json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.nodeRecords[runID+":"+nodeID]
	if !ok {
		return "", nil, nil
	}
	return v.status, append(json.RawMessage(nil), v.output...), nil
}
func (s *memoryStore) RecoverNode(_ context.Context, runID, nodeID string, out json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeRecords[runID+":"+nodeID] = memoryNodeRecord{status: "succeeded", output: append(json.RawMessage(nil), out...)}
	return nil
}
func (s *memoryStore) ResetNode(_ context.Context, runID, nodeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.nodeRecords, runID+":"+nodeID)
	return nil
}

func TestWorkflowConditionAndApproval(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	agent := &fakeAgent{}
	engine, err := New(store, agent, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := wf.Definition{ID: "demo", Name: "demo", Version: 1, Entry: "agent", Nodes: []wf.Node{{ID: "agent", Type: wf.NodeAgent, Agent: &wf.AgentConfig{ModelID: "model-a", WorkspaceID: "ws", Prompt: "review"}}, {ID: "gate", Type: wf.NodeCondition, Condition: &wf.ConditionConfig{InputKey: "approved", Equals: true}}, {ID: "approval", Type: wf.NodeApproval, Approval: &wf.ApprovalConfig{Message: "human approval"}}, {ID: "end", Type: wf.NodeEnd}, {ID: "reject-end", Type: wf.NodeEnd}}, Edges: []wf.Edge{{From: "agent", To: "gate"}, {From: "gate", To: "approval", Handle: "true"}, {From: "gate", To: "reject-end", Handle: "false"}, {From: "approval", To: "end"}}}
	if err = engine.PutDefinition(ctx, d); err != nil {
		t.Fatal(err)
	}
	r, err := engine.Start(ctx, "demo", "run-1", map[string]any{"approved": true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != wf.RunWaitingApproval {
		t.Fatalf("want waiting approval, got %s", r.Status)
	}
	r, err = engine.Resume(ctx, r.ID, true, "looks good")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != wf.RunSucceeded {
		t.Fatalf("want succeeded, got %s", r.Status)
	}
	if agent.calls != 1 {
		t.Fatalf("agent calls=%d", agent.calls)
	}
}
func TestWorkflowConditionFalseBranch(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	engine, _ := New(store, &fakeAgent{}, nil)
	d := wf.Definition{ID: "branch", Name: "branch", Version: 1, Entry: "condition", Nodes: []wf.Node{{ID: "condition", Type: wf.NodeCondition, Condition: &wf.ConditionConfig{InputKey: "allow", Equals: true}}, {ID: "yes", Type: wf.NodeEnd}, {ID: "no", Type: wf.NodeEnd}}, Edges: []wf.Edge{{From: "condition", To: "yes", Handle: "true"}, {From: "condition", To: "no", Handle: "false"}}}
	if err := engine.PutDefinition(ctx, d); err != nil {
		t.Fatal(err)
	}
	r, err := engine.Start(ctx, "branch", "run-2", map[string]any{"allow": false})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != wf.RunSucceeded || r.Nodes["no"].Status != wf.NodeSucceeded {
		t.Fatalf("unexpected run: %+v", r)
	}
}

var _ = json.RawMessage{}
var _ = time.Now

type parallelAgent struct {
	active atomic.Int32
	max    atomic.Int32
	calls  atomic.Int32
}

func (p *parallelAgent) Run(ctx context.Context, task agentdom.Task) (agentdom.Result, error) {
	current := p.active.Add(1)
	defer p.active.Add(-1)
	p.calls.Add(1)
	for {
		old := p.max.Load()
		if current <= old || p.max.CompareAndSwap(old, current) {
			break
		}
	}
	select {
	case <-ctx.Done():
		return agentdom.Result{}, ctx.Err()
	case <-time.After(40 * time.Millisecond):
	}
	return agentdom.Result{TaskID: task.ID, Status: agentdom.StatusSucceeded, Content: task.Prompt}, nil
}
func TestWorkflowParallelFanOutAndJoin(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	agent := &parallelAgent{}
	engine, err := New(store, agent, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := wf.Definition{ID: "parallel", Name: "parallel", Version: 1, Entry: "root", Nodes: []wf.Node{
		{ID: "root", Type: wf.NodeAgent, Agent: &wf.AgentConfig{ModelID: "m", WorkspaceID: "w", Prompt: "root"}},
		{ID: "left", Type: wf.NodeAgent, Agent: &wf.AgentConfig{ModelID: "m", WorkspaceID: "w", Prompt: "left"}},
		{ID: "right", Type: wf.NodeAgent, Agent: &wf.AgentConfig{ModelID: "m", WorkspaceID: "w", Prompt: "right"}},
		{ID: "join", Type: wf.NodeAgent, Agent: &wf.AgentConfig{ModelID: "m", WorkspaceID: "w", Prompt: "join"}},
		{ID: "end", Type: wf.NodeEnd},
	}, Edges: []wf.Edge{{From: "root", To: "left"}, {From: "root", To: "right"}, {From: "left", To: "join"}, {From: "right", To: "join"}, {From: "join", To: "end"}}}
	if err = engine.PutDefinition(ctx, d); err != nil {
		t.Fatal(err)
	}
	r, err := engine.Start(ctx, "parallel", "parallel-run", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != wf.RunSucceeded {
		t.Fatalf("status=%s error=%s pending=%v", r.Status, r.Error, r.PendingNodes)
	}
	if agent.calls.Load() != 4 {
		t.Fatalf("agent calls=%d", agent.calls.Load())
	}
	if agent.max.Load() < 2 {
		t.Fatalf("expected parallel execution, max concurrency=%d", agent.max.Load())
	}
}

func TestWorkflowWorkerProcessesQueuedRun(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	agent := &fakeAgent{}
	engine, err := New(store, agent, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := wf.Definition{ID: "queued", Name: "queued", Version: 1, Entry: "agent", Nodes: []wf.Node{{ID: "agent", Type: wf.NodeAgent, Agent: &wf.AgentConfig{ModelID: "m", WorkspaceID: "w", Prompt: "run"}}, {ID: "end", Type: wf.NodeEnd}}, Edges: []wf.Edge{{From: "agent", To: "end"}}}
	if err = engine.PutDefinition(ctx, d); err != nil {
		t.Fatal(err)
	}
	r, err := engine.StartQueued(ctx, "queued", "queued-run", nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != wf.RunQueued {
		t.Fatalf("expected queued, got %s", r.Status)
	}
	worker, err := NewWorker(store, engine, WorkerConfig{ID: "worker-test", LeaseDuration: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.runOne(ctx); err != nil {
		t.Fatal(err)
	}
	r, err = engine.GetRun(ctx, "queued-run")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != wf.RunSucceeded {
		t.Fatalf("expected succeeded, got %s (%s)", r.Status, r.Error)
	}
}

type snapshotAgent struct {
	fakeAgent
	snapshot    agentdom.Result
	snapshotErr error
}

func (a *snapshotAgent) Get(id string) (agentdom.Result, error) { return a.snapshot, a.snapshotErr }
func TestWorkflowWorkerReconcilesCompletedAgentSnapshot(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	agent := &snapshotAgent{snapshot: agentdom.Result{TaskID: stableTaskID("recover-run", "agent"), Status: agentdom.StatusSucceeded, Content: "recovered"}}
	engine, err := New(store, agent, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := wf.Definition{ID: "recover", Name: "recover", Version: 1, Entry: "agent", Nodes: []wf.Node{{ID: "agent", Type: wf.NodeAgent, Agent: &wf.AgentConfig{ModelID: "m", WorkspaceID: "w", Prompt: "run"}}, {ID: "end", Type: wf.NodeEnd}}, Edges: []wf.Edge{{From: "agent", To: "end"}}}
	if err = engine.PutDefinition(ctx, d); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r := wf.Run{ID: "recover-run", WorkflowID: "recover", WorkflowVersion: 1, Status: wf.RunQueued, CurrentNode: "agent", Context: map[string]any{}, Nodes: map[string]wf.NodeState{"agent": {Status: wf.NodeRunning, IdempotencyKey: "recover-run:agent"}}, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.nodeRecords["recover-run:agent"] = memoryNodeRecord{status: "running"}
	store.jobs = append(store.jobs, "recover-run")
	store.mu.Unlock()
	worker, err := NewWorker(store, engine, WorkerConfig{ID: "recovery-worker", LeaseDuration: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.runOne(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := engine.GetRun(ctx, "recover-run")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != wf.RunSucceeded {
		t.Fatalf("status=%s error=%s", got.Status, got.Error)
	}
	if agent.calls != 0 {
		t.Fatalf("completed Agent task was replayed: calls=%d", agent.calls)
	}
}

func TestWorkflowQueuedApprovalContinuation(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	agent := &fakeAgent{}
	engine, err := New(store, agent, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := wf.Definition{ID: "queued-approval", Name: "queued approval", Version: 1, Entry: "agent", Nodes: []wf.Node{
		{ID: "agent", Type: wf.NodeAgent, Agent: &wf.AgentConfig{ModelID: "m", WorkspaceID: "w", Prompt: "review"}},
		{ID: "approval", Type: wf.NodeApproval, Approval: &wf.ApprovalConfig{Message: "confirm"}},
		{ID: "end", Type: wf.NodeEnd},
	}, Edges: []wf.Edge{{From: "agent", To: "approval"}, {From: "approval", To: "end"}}}
	if err = engine.PutDefinition(ctx, d); err != nil {
		t.Fatal(err)
	}
	r, err := engine.StartQueued(ctx, "queued-approval", "approval-run", nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(store, engine, WorkerConfig{ID: "approval-worker", LeaseDuration: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.runOne(ctx); err != nil {
		t.Fatal(err)
	}
	r, err = engine.GetRun(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != wf.RunWaitingApproval {
		t.Fatalf("expected approval wait, got %s", r.Status)
	}
	r, err = engine.ResumeQueued(ctx, r.ID, true, "approved", 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != wf.RunQueued {
		t.Fatalf("expected queued continuation, got %s", r.Status)
	}
	if err = worker.runOne(ctx); err != nil {
		t.Fatal(err)
	}
	r, err = engine.GetRun(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != wf.RunSucceeded {
		t.Fatalf("expected success, got %s (%s)", r.Status, r.Error)
	}
}
