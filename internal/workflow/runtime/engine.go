package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	agentdom "github.com/thkx/agentmesh/internal/domain/agent"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/workflow/capability"
	"github.com/thkx/agentmesh/internal/workflow/checkpoint"
	"github.com/thkx/agentmesh/internal/workflow/compiler"
	"github.com/thkx/agentmesh/internal/workflow/policy"
)

type AgentRunner interface {
	Run(context.Context, agentdom.Task) (agentdom.Result, error)
}
type AgentResultReader interface {
	Get(string) (agentdom.Result, error)
}
type QueueStore interface {
	EnqueueRun(context.Context, string, int) error
	RequeueRun(context.Context, string, int) error
	ClaimRun(context.Context, string, time.Duration) (checkpoint.Job, error)
	HeartbeatRun(context.Context, checkpoint.Job, time.Duration) error
	FinishRun(context.Context, checkpoint.Job, wf.Run, error) error
}
type FencedRunStore interface {
	SaveRunFenced(context.Context, wf.Run, string, int64) error
}
type NodeLedger interface {
	BeginNode(context.Context, string, string, string, string, int64, time.Duration) (bool, json.RawMessage, error)
	CompleteNode(context.Context, string, string, string, int64, json.RawMessage, error) error
	LookupNode(context.Context, string, string) (string, json.RawMessage, error)
	RecoverNode(context.Context, string, string, json.RawMessage) error
	ResetNode(context.Context, string, string) error
}
type Store interface {
	SaveDefinition(context.Context, wf.Definition) error
	GetDefinition(context.Context, string, int) (wf.Definition, error)
	CreateRun(context.Context, wf.Run) error
	SaveRun(context.Context, wf.Run) error
	GetRun(context.Context, string) (wf.Run, error)
	ListRuns(context.Context, string, int) ([]wf.Run, error)
}
type Engine struct {
	store        Store
	agent        AgentRunner
	policy       policy.Policy
	capabilities *capability.Registry
	locks        sync.Map
}

func New(store Store, agent AgentRunner, p policy.Policy) (*Engine, error) {
	if store == nil || agent == nil {
		return nil, errors.New("workflow engine dependencies are nil")
	}
	if p == nil {
		p = policy.AllowAll{}
	}
	return &Engine{store: store, agent: agent, policy: p, capabilities: capability.NewRegistry()}, nil
}

type EventReader interface {
	Events(context.Context, string, int64, int) ([]wf.Event, error)
}
type ObservationReader interface {
	NodeObservations(context.Context, string) ([]wf.NodeObservation, error)
}
type NodeRecovery interface {
	RecoverFailedNode(context.Context, string, string, string, string, int) (wf.Run, error)
}

func (e *Engine) Events(ctx context.Context, runID string, after int64, limit int) ([]wf.Event, error) {
	if _, err := e.store.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	s, ok := e.store.(EventReader)
	if !ok {
		return nil, errors.New("workflow store does not support event history")
	}
	return s.Events(ctx, runID, after, limit)
}
func (e *Engine) NodeObservations(ctx context.Context, runID string) ([]wf.NodeObservation, error) {
	if _, err := e.store.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	s, ok := e.store.(ObservationReader)
	if !ok {
		return nil, errors.New("workflow store does not support node observations")
	}
	return s.NodeObservations(ctx, runID)
}
func (e *Engine) RecoverNode(ctx context.Context, runID, nodeID, action, comment string, maxAttempts int) (wf.Run, error) {
	s, ok := e.store.(NodeRecovery)
	if !ok {
		return wf.Run{}, errors.New("workflow store does not support operator recovery")
	}
	return s.RecoverFailedNode(ctx, runID, nodeID, action, comment, maxAttempts)
}

func (e *Engine) SetCapabilities(r *capability.Registry) {
	if r != nil {
		e.capabilities = r
	}
}

func (e *Engine) PutDefinition(ctx context.Context, d wf.Definition) error {
	if _, err := compiler.Compile(d); err != nil {
		return err
	}
	return e.store.SaveDefinition(ctx, d)
}
func (e *Engine) Definition(ctx context.Context, id string, version int) (wf.Definition, error) {
	return e.store.GetDefinition(ctx, id, version)
}
func (e *Engine) Start(ctx context.Context, workflowID, runID string, input map[string]any) (wf.Run, error) {
	d, err := e.store.GetDefinition(ctx, workflowID, 0)
	if err != nil {
		return wf.Run{}, err
	}
	c, err := compiler.Compile(d)
	if err != nil {
		return wf.Run{}, err
	}
	if runID == "" {
		runID = newID()
	}
	now := time.Now().UTC()
	b, err := json.Marshal(input)
	if err != nil {
		return wf.Run{}, err
	}
	r := wf.Run{ID: runID, WorkflowID: d.ID, WorkflowVersion: d.Version, Status: wf.RunQueued, CurrentNode: d.Entry, Input: b, Context: input, Nodes: map[string]wf.NodeState{}, CreatedAt: now, UpdatedAt: now}
	if r.Context == nil {
		r.Context = map[string]any{}
	}
	if err = e.store.CreateRun(ctx, r); err != nil {
		return wf.Run{}, err
	}
	persisted, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return wf.Run{}, err
	}
	return e.execute(ctx, c, persisted)
}

// StartQueued persists a run and enqueues it for a durable background worker.
// It returns immediately; callers poll GetRun for progress.
func (e *Engine) StartQueued(ctx context.Context, workflowID, runID string, input map[string]any, maxAttempts int) (wf.Run, error) {
	d, err := e.store.GetDefinition(ctx, workflowID, 0)
	if err != nil {
		return wf.Run{}, err
	}
	if _, err = compiler.Compile(d); err != nil {
		return wf.Run{}, err
	}
	if runID == "" {
		runID = newID()
	}
	now := time.Now().UTC()
	b, err := json.Marshal(input)
	if err != nil {
		return wf.Run{}, err
	}
	r := wf.Run{ID: runID, WorkflowID: d.ID, WorkflowVersion: d.Version, Status: wf.RunQueued, CurrentNode: d.Entry, Input: b, Context: input, Nodes: map[string]wf.NodeState{}, CreatedAt: now, UpdatedAt: now}
	if r.Context == nil {
		r.Context = map[string]any{}
	}
	if err = e.store.CreateRun(ctx, r); err != nil {
		return wf.Run{}, err
	}
	qs, ok := e.store.(QueueStore)
	if !ok {
		return wf.Run{}, errors.New("workflow store does not support durable queue")
	}
	if err = qs.EnqueueRun(ctx, runID, maxAttempts); err != nil {
		return wf.Run{}, err
	}
	return e.store.GetRun(ctx, runID)
}

// Process executes an already persisted run. It is called only by a leased worker.
func (e *Engine) persistRun(ctx context.Context, run wf.Run, owner string, fenceToken int64) error {
	if fenceToken > 0 {
		if store, ok := e.store.(FencedRunStore); ok {
			return store.SaveRunFenced(ctx, run, owner, fenceToken)
		}
	}
	return e.store.SaveRun(ctx, run)
}

func (e *Engine) Process(ctx context.Context, runID, owner string, fenceTokens ...int64) (wf.Run, error) {
	var fenceToken int64
	if len(fenceTokens) > 0 {
		fenceToken = fenceTokens[0]
	}
	return e.ProcessLeased(ctx, runID, owner, fenceToken, 30*time.Second)
}

// ProcessLeased runs a workflow using the same lease horizon as its queue claim.
func (e *Engine) ProcessLeased(ctx context.Context, runID, owner string, fenceToken int64, lease time.Duration) (wf.Run, error) {
	if lease < time.Second {
		lease = 30 * time.Second
	}
	r, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return r, err
	}
	if r.Status != wf.RunQueued && r.Status != wf.RunRunning {
		return r, wf.ErrInvalidTransition
	}
	if r.Context == nil {
		r.Context = map[string]any{}
	}
	if r.Nodes == nil {
		r.Nodes = map[string]wf.NodeState{}
	}
	d, err := e.store.GetDefinition(ctx, r.WorkflowID, r.WorkflowVersion)
	if err != nil {
		return r, err
	}
	c, err := compiler.Compile(d)
	if err != nil {
		return r, err
	}
	if err = e.reconcileAgentNodes(ctx, c, &r, owner, fenceToken); err != nil {
		return r, err
	}
	return e.executeOwned(ctx, c, r, owner, fenceToken, lease)
}
func (e *Engine) reconcileAgentNodes(ctx context.Context, c *compiler.Compiled, r *wf.Run, owner string, fenceToken int64) error {
	if r.Context == nil {
		r.Context = map[string]any{}
	}
	if r.Nodes == nil {
		r.Nodes = map[string]wf.NodeState{}
	}
	ledger, hasLedger := e.store.(NodeLedger)
	reader, canRead := e.agent.(AgentResultReader)
	for nodeID, state := range r.Nodes {
		if state.Status != wf.NodeRunning {
			continue
		}
		node, ok := c.Nodes[nodeID]
		if !ok {
			return fmt.Errorf("checkpoint references unknown node %s", nodeID)
		}
		if !hasLedger {
			state.Status = wf.NodeInterrupted
			state.Error = "node outcome is indeterminate after worker lease expiry; refusing replay"
			r.Nodes[nodeID] = state
			r.Status = wf.RunFailed
			r.Error = state.Error
			r.FinishedAt = time.Now().UTC()
			_ = e.persistRun(context.WithoutCancel(ctx), *r, owner, fenceToken)
			return errors.New(state.Error)
		}
		ledgerStatus, ledgerOutput, err := ledger.LookupNode(ctx, r.ID, nodeID)
		if err != nil {
			return err
		}
		if ledgerStatus == "succeeded" && len(ledgerOutput) > 0 {
			state.Status = wf.NodeSucceeded
			state.Output = ledgerOutput
			state.FinishedAt = time.Now().UTC()
			r.Nodes[nodeID] = state
			if node.Type == wf.NodeAgent {
				var result agentdom.Result
				if err = json.Unmarshal(ledgerOutput, &result); err != nil {
					return err
				}
				r.Context[nodeID] = map[string]any{"content": result.Content, "status": result.Status, "task_id": result.TaskID}
			}
			for _, edge := range c.Outgoing[nodeID] {
				if node.Type != wf.NodeCondition || edgeIsActive(node, state, edge) {
					r.PendingNodes = append(r.PendingNodes, edge.To)
				}
			}
			continue
		}
		if node.Type == wf.NodeCondition || node.Type == wf.NodeEnd {
			if ledgerStatus == "running" || ledgerStatus == "failed" {
				if err = ledger.ResetNode(ctx, r.ID, nodeID); err != nil {
					return err
				}
			}
			if ledgerStatus == "" || ledgerStatus == "running" || ledgerStatus == "failed" {
				state.Status = wf.NodePending
				state.Error = ""
				state.Output = nil
				r.Nodes[nodeID] = state
				r.PendingNodes = append(r.PendingNodes, nodeID)
				continue
			}
			state.Status = wf.NodeInterrupted
			state.Error = "pure node checkpoint has an invalid state"
			r.Nodes[nodeID] = state
			r.Status = wf.RunFailed
			r.Error = state.Error
			r.FinishedAt = time.Now().UTC()
			_ = e.persistRun(context.WithoutCancel(ctx), *r, owner, fenceToken)
			return errors.New(state.Error)
		}
		if node.Type != wf.NodeAgent || !canRead {
			state.Status = wf.NodeInterrupted
			state.Error = "node outcome is indeterminate after worker lease expiry; refusing replay"
			r.Nodes[nodeID] = state
			r.Status = wf.RunFailed
			r.Error = state.Error
			r.FinishedAt = time.Now().UTC()
			_ = e.persistRun(context.WithoutCancel(ctx), *r, owner, fenceToken)
			return errors.New(state.Error)
		}
		result, readErr := reader.Get(stableTaskID(r.ID, nodeID))
		if readErr == nil && result.Status == agentdom.StatusSucceeded {
			b, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				return marshalErr
			}
			if ledgerStatus == "running" || ledgerStatus == "failed" {
				if err = ledger.RecoverNode(ctx, r.ID, nodeID, b); err != nil {
					return err
				}
			}
			state.Status = wf.NodeSucceeded
			state.Output = b
			state.FinishedAt = time.Now().UTC()
			state.Error = ""
			r.Nodes[nodeID] = state
			r.Context[nodeID] = map[string]any{"content": result.Content, "status": result.Status, "task_id": result.TaskID}
			for _, edge := range c.Outgoing[nodeID] {
				r.PendingNodes = append(r.PendingNodes, edge.To)
			}
			continue
		}
		if (errors.Is(readErr, agentdom.ErrTaskNotFound) || (readErr == nil && result.Status == agentdom.StatusFailed && result.ToolCalls == 0)) && (ledgerStatus == "running" || ledgerStatus == "failed" || ledgerStatus == "") {
			if ledgerStatus == "running" || ledgerStatus == "failed" {
				if err = ledger.ResetNode(ctx, r.ID, nodeID); err != nil {
					return err
				}
			}
			state.Status = wf.NodePending
			state.Error = ""
			state.Output = nil
			r.Nodes[nodeID] = state
			r.PendingNodes = append(r.PendingNodes, nodeID)
			continue
		}
		state.Status = wf.NodeInterrupted
		state.Error = "Agent task outcome is indeterminate or may contain tool side effects; automatic replay refused"
		r.Nodes[nodeID] = state
		r.Status = wf.RunFailed
		r.Error = state.Error
		r.FinishedAt = time.Now().UTC()
		_ = e.persistRun(context.WithoutCancel(ctx), *r, owner, fenceToken)
		return errors.New(state.Error)
	}
	r.PendingNodes = unique(r.PendingNodes)
	if err := e.persistRun(ctx, *r, owner, fenceToken); err != nil {
		return err
	}
	return nil
}

func (e *Engine) Resume(ctx context.Context, runID string, approved bool, comment string) (wf.Run, error) {
	lockAny, _ := e.locks.LoadOrStore(runID, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	locked := true
	defer func() {
		if locked {
			lock.Unlock()
		}
	}()
	r, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return r, err
	}
	if r.Context == nil {
		r.Context = map[string]any{}
	}
	if r.Nodes == nil {
		r.Nodes = map[string]wf.NodeState{}
	}
	if r.Status != wf.RunWaitingApproval {
		return r, wf.ErrInvalidTransition
	}
	d, err := e.store.GetDefinition(ctx, r.WorkflowID, r.WorkflowVersion)
	if err != nil {
		return r, err
	}
	c, err := compiler.Compile(d)
	if err != nil {
		return r, err
	}
	n := c.Nodes[r.CurrentNode]
	if n.Type != wf.NodeApproval {
		return r, wf.ErrInvalidTransition
	}
	state := r.Nodes[n.ID]
	state.FinishedAt = time.Now().UTC()
	if approved {
		state.Status = wf.NodeSucceeded
		state.Output, _ = json.Marshal(map[string]any{"approved": true, "comment": comment})
		r.Context[n.ID] = map[string]any{"approved": true, "comment": comment}
		r.Status = wf.RunRunning
		r.PendingNodes = append(r.PendingNodes, outgoingIDs(c, n.ID)...)
		r.CurrentNode = ""
	} else {
		state.Status = wf.NodeSucceeded
		state.Output, _ = json.Marshal(map[string]any{"approved": false, "comment": comment})
		r.Nodes[n.ID] = state
		r.Status = wf.RunRejected
		r.Error = "approval rejected"
		r.FinishedAt = time.Now().UTC()
		r.ApprovalMessage = ""
		if err = e.store.SaveRun(ctx, r); err != nil {
			return r, err
		}
		return r, nil
	}
	r.Nodes[n.ID] = state
	r.ApprovalMessage = ""
	if err = e.store.SaveRun(ctx, r); err != nil {
		return r, err
	}
	lock.Unlock()
	locked = false
	return e.execute(ctx, c, r)
}

// ResumeQueued persists an approval decision and re-enqueues the workflow continuation.
func (e *Engine) ResumeQueued(ctx context.Context, runID string, approved bool, comment string, maxAttempts int) (wf.Run, error) {
	lockAny, _ := e.locks.LoadOrStore(runID, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	r, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return r, err
	}
	if r.Context == nil {
		r.Context = map[string]any{}
	}
	if r.Nodes == nil {
		r.Nodes = map[string]wf.NodeState{}
	}
	if r.Status != wf.RunWaitingApproval {
		return r, wf.ErrInvalidTransition
	}
	d, err := e.store.GetDefinition(ctx, r.WorkflowID, r.WorkflowVersion)
	if err != nil {
		return r, err
	}
	c, err := compiler.Compile(d)
	if err != nil {
		return r, err
	}
	n, ok := c.Nodes[r.CurrentNode]
	if !ok || n.Type != wf.NodeApproval {
		return r, wf.ErrInvalidTransition
	}
	state := r.Nodes[n.ID]
	state.Status = wf.NodeSucceeded
	state.FinishedAt = time.Now().UTC()
	state.Output, _ = json.Marshal(map[string]any{"approved": approved, "comment": comment})
	r.Nodes[n.ID] = state
	r.Context[n.ID] = map[string]any{"approved": approved, "comment": comment}
	r.ApprovalMessage = ""
	if !approved {
		r.Status = wf.RunRejected
		r.Error = "approval rejected"
		r.FinishedAt = time.Now().UTC()
		if err = e.store.SaveRun(ctx, r); err != nil {
			return r, err
		}
		return r, nil
	}
	r.PendingNodes = append(r.PendingNodes, outgoingIDs(c, n.ID)...)
	r.PendingNodes = unique(r.PendingNodes)
	r.CurrentNode = ""
	r.Status = wf.RunQueued
	r.Error = ""
	r.FinishedAt = time.Time{}
	if err = e.store.SaveRun(ctx, r); err != nil {
		return r, err
	}
	qs, ok := e.store.(QueueStore)
	if !ok {
		return r, errors.New("workflow store does not support durable queue")
	}
	if err = qs.RequeueRun(ctx, runID, maxAttempts); err != nil {
		return r, err
	}
	return r, nil
}

func (e *Engine) GetRun(ctx context.Context, id string) (wf.Run, error) {
	return e.store.GetRun(ctx, id)
}
func (e *Engine) ListRuns(ctx context.Context, id string, limit int) ([]wf.Run, error) {
	return e.store.ListRuns(ctx, id, limit)
}
func (e *Engine) execute(ctx context.Context, c *compiler.Compiled, r wf.Run) (wf.Run, error) {
	return e.executeOwned(ctx, c, r, newID(), 0, 24*time.Hour)
}
func (e *Engine) executeOwned(ctx context.Context, c *compiler.Compiled, r wf.Run, owner string, fenceToken int64, nodeLease time.Duration) (wf.Run, error) {
	lockAny, _ := e.locks.LoadOrStore(r.ID, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	if r.Context == nil {
		r.Context = map[string]any{}
	}
	if r.Nodes == nil {
		r.Nodes = map[string]wf.NodeState{}
	}
	if len(r.PendingNodes) == 0 && r.CurrentNode != "" {
		r.PendingNodes = []string{r.CurrentNode}
	}
	r.Status = wf.RunRunning
	if err := e.persistRun(ctx, r, owner, fenceToken); err != nil {
		return r, err
	}
	ledger, hasLedger := e.store.(NodeLedger)
	for {
		if err := ctx.Err(); err != nil {
			r.Status = wf.RunFailed
			r.Error = err.Error()
			r.FinishedAt = time.Now().UTC()
			_ = e.persistRun(context.WithoutCancel(ctx), r, owner, fenceToken)
			return r, err
		}
		pending := unique(r.PendingNodes)
		r.PendingNodes = pending
		if len(pending) == 0 {
			r.Status = wf.RunSucceeded
			r.CurrentNode = ""
			r.FinishedAt = time.Now().UTC()
			r.UpdatedAt = r.FinishedAt
			if err := e.persistRun(ctx, r, owner, fenceToken); err != nil {
				return r, err
			}
			return r, nil
		}
		ready := make([]string, 0, len(pending))
		progress := false
		for _, id := range pending {
			_, ok := c.Nodes[id]
			if !ok {
				return r, fmt.Errorf("node %s missing", id)
			}
			state := r.Nodes[id]
			if state.Status == wf.NodeSucceeded || state.Status == wf.NodeSkipped {
				progress = true
				continue
			}
			if state.Status == wf.NodeRunning || state.Status == wf.NodeInterrupted {
				state.Status = wf.NodeInterrupted
				state.Error = "node execution outcome is indeterminate; automatic replay refused"
				r.Nodes[id] = state
				r.Status = wf.RunFailed
				r.Error = state.Error
				r.FinishedAt = time.Now().UTC()
				_ = e.persistRun(context.WithoutCancel(ctx), r, owner, fenceToken)
				return r, errors.New(state.Error)
			}
			parentsResolved, active := true, false
			for parent, edges := range c.Outgoing {
				for _, edge := range edges {
					if edge.To != id {
						continue
					}
					ps := r.Nodes[parent].Status
					if ps != wf.NodeSucceeded && ps != wf.NodeSkipped {
						parentsResolved = false
					}
					parentState := r.Nodes[parent]
					if (ps == wf.NodeSucceeded || (ps == wf.NodeSkipped && parentState.ManualSkip)) && edgeIsActive(c.Nodes[parent], parentState, edge) {
						active = true
					}
				}
			}
			if !parentsResolved {
				continue
			}
			if !active && id != c.Definition.Entry {
				state.Status = wf.NodeSkipped
				state.FinishedAt = time.Now().UTC()
				r.Nodes[id] = state
				r.PendingNodes = append(r.PendingNodes, outgoingIDs(c, id)...)
				progress = true
				continue
			}
			ready = append(ready, id)
		}
		// Remove already-resolved nodes and persist propagated skips.
		remaining := make([]string, 0, len(r.PendingNodes))
		for _, id := range r.PendingNodes {
			st := r.Nodes[id].Status
			if st != wf.NodeSucceeded && st != wf.NodeSkipped {
				remaining = append(remaining, id)
			}
		}
		r.PendingNodes = unique(remaining)
		if len(ready) == 0 {
			if progress {
				if err := e.persistRun(ctx, r, owner, fenceToken); err != nil {
					return r, err
				}
				continue
			}
			// A DAG with unresolved predecessors indicates corrupt/incomplete checkpoint state.
			r.Status = wf.RunFailed
			r.Error = "workflow graph is blocked: no executable node is ready"
			r.FinishedAt = time.Now().UTC()
			_ = e.persistRun(context.WithoutCancel(ctx), r, owner, fenceToken)
			return r, errors.New(r.Error)
		}
		// Claim/checkpoint every node before starting any side effects.
		type work struct {
			id     string
			cached json.RawMessage
		}
		// Approval is a graph-wide barrier: do not start sibling nodes until a human decision.
		for _, id := range ready {
			if c.Nodes[id].Type == wf.NodeApproval {
				ready = []string{id}
				break
			}
		}
		works := make([]work, 0, len(ready))
		for _, id := range ready {
			n := c.Nodes[id]
			if err := e.policyCheck(ctx, r.ID, n.ID); err != nil {
				r.Status = wf.RunFailed
				r.Error = err.Error()
				r.FinishedAt = time.Now().UTC()
				_ = e.persistRun(context.WithoutCancel(ctx), r, owner, fenceToken)
				return r, err
			}
			state := r.Nodes[id]
			if n.Type == wf.NodeApproval {
				state.Status = wf.NodeWaitingApproval
				state.StartedAt = time.Now().UTC()
				r.Nodes[id] = state
				r.Status = wf.RunWaitingApproval
				r.CurrentNode = id
				r.ApprovalMessage = n.Approval.Message
				r.PendingNodes = removeIDs(r.PendingNodes, []string{id})
				if err := e.persistRun(ctx, r, owner, fenceToken); err != nil {
					return r, err
				}
				return r, nil
			}
			state.Status = wf.NodeRunning
			state.StartedAt = time.Now().UTC()
			state.Attempt++
			state.WorkerID = owner
			state.Error = ""
			state.IdempotencyKey = r.ID + ":" + id
			r.Nodes[id] = state
			works = append(works, work{id: id})
		}
		r.CurrentNode = ready[0]
		r.PendingNodes = removeIDs(r.PendingNodes, ready)
		if err := e.persistRun(ctx, r, owner, fenceToken); err != nil {
			return r, err
		}
		// The running checkpoint is durable before creating any node execution intent.
		for i := range works {
			n := c.Nodes[works[i].id]
			if hasLedger {
				done, output, beginErr := ledger.BeginNode(ctx, r.ID, works[i].id, r.ID+":"+works[i].id, owner, fenceToken, nodeLease)
				if beginErr != nil {
					state := r.Nodes[works[i].id]
					state.Status = wf.NodeInterrupted
					state.Error = beginErr.Error()
					r.Nodes[works[i].id] = state
					r.Status = wf.RunFailed
					r.Error = beginErr.Error()
					r.FinishedAt = time.Now().UTC()
					_ = e.persistRun(context.WithoutCancel(ctx), r, owner, fenceToken)
					return r, beginErr
				}
				if done {
					works[i].cached = output
				}
			} else if n.Type == wf.NodeAgent { /* in-memory stores are used by unit tests; production always has the durable ledger */
			}
		}
		// Independent nodes in the same DAG wave execute concurrently.
		type result struct {
			id           string
			output       json.RawMessage
			contextValue any
			branch       string
			waiting      bool
			err          error
		}
		results := make(chan result, len(works))
		var wg sync.WaitGroup
		parallel := make(chan struct{}, 8)
		for _, w := range works {
			w := w
			wg.Add(1)
			go func() {
				defer wg.Done()
				n := c.Nodes[w.id]
				if len(w.cached) > 0 {
					out, cv, branch, err := cachedNodeResult(n, w.cached)
					results <- result{id: w.id, output: w.cached, contextValue: cv, branch: branch, err: err}
					_ = out
					return
				}
				select {
				case parallel <- struct{}{}:
				case <-ctx.Done():
					results <- result{id: w.id, err: ctx.Err()}
					return
				}
				out, cv, branch, err := e.executeNode(ctx, n, r.Context, stableTaskID(r.ID, n.ID))
				<-parallel
				results <- result{id: w.id, output: out, contextValue: cv, branch: branch, err: err}
			}()
		}
		wg.Wait()
		close(results)
		failed := error(nil)
		for res := range results {
			state := r.Nodes[res.id]
			if res.waiting {
				state.Status = wf.NodeWaitingApproval
				state.FinishedAt = time.Time{}
				r.Nodes[res.id] = state
				r.Status = wf.RunWaitingApproval
				r.CurrentNode = res.id
				r.ApprovalMessage = c.Nodes[res.id].Approval.Message
				if err := e.persistRun(ctx, r, owner, fenceToken); err != nil {
					return r, err
				}
				return r, nil
			}
			if res.err != nil {
				state.Status = wf.NodeFailed
				state.Error = res.err.Error()
				state.FinishedAt = time.Now().UTC()
				if !state.StartedAt.IsZero() {
					state.DurationMillis = state.FinishedAt.Sub(state.StartedAt).Milliseconds()
				}
				failed = res.err
			} else {
				state.Status = wf.NodeSucceeded
				state.Output = res.output
				state.FinishedAt = time.Now().UTC()
				if !state.StartedAt.IsZero() {
					state.DurationMillis = state.FinishedAt.Sub(state.StartedAt).Milliseconds()
				}
				if res.contextValue != nil {
					r.Context[res.id] = res.contextValue
				}
			}
			r.Nodes[res.id] = state
			if hasLedger {
				if err := ledger.CompleteNode(ctx, r.ID, res.id, owner, fenceToken, res.output, res.err); err != nil && failed == nil {
					failed = err
				}
			}
			if res.err == nil {
				for _, edge := range c.Outgoing[res.id] {
					if edgeIsActive(c.Nodes[res.id], state, edge) {
						r.PendingNodes = append(r.PendingNodes, edge.To)
					}
				}
			}
		}
		if failed != nil {
			r.Status = wf.RunFailed
			r.Error = failed.Error()
			r.FinishedAt = time.Now().UTC()
			_ = e.persistRun(context.WithoutCancel(ctx), r, owner, fenceToken)
			return r, failed
		}
		r.PendingNodes = unique(r.PendingNodes)
		if err := e.persistRun(ctx, r, owner, fenceToken); err != nil {
			return r, err
		}
	}
}

func (e *Engine) policyCheck(ctx context.Context, runID, nodeID string) error {
	decision, err := e.policy.AuthorizeNode(ctx, runID, nodeID)
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return fmt.Errorf("policy denied node %s: %s", nodeID, decision.Reason)
	}
	return nil
}
func (e *Engine) executeNode(ctx context.Context, n wf.Node, contextData map[string]any, taskID string) (json.RawMessage, any, string, error) {
	switch n.Type {
	case wf.NodeAgent:
		cfg := *n.Agent
		cfg.ApprovedTools = append([]string(nil), cfg.ApprovedTools...)
		if err := e.capabilities.Apply(cfg.Capabilities, &cfg); err != nil {
			return nil, nil, "", err
		}
		task := agentdom.Task{ID: taskID, ModelID: cfg.ModelID, WorkspaceID: cfg.WorkspaceID, Prompt: render(cfg.Prompt, contextData), MaxSteps: cfg.MaxSteps, MaxToolCalls: cfg.MaxToolCalls, ApprovedTools: cfg.ApprovedTools}
		result, err := e.agent.Run(ctx, task)
		if err != nil {
			return nil, nil, "", err
		}
		b, err := json.Marshal(result)
		return b, map[string]any{"content": result.Content, "status": result.Status, "task_id": result.TaskID}, "", err
	case wf.NodeCondition:
		actual := lookup(contextData, n.Condition.InputKey)
		matched := reflect.DeepEqual(actual, n.Condition.Equals)
		branch := "false"
		if matched {
			branch = "true"
		}
		b, err := json.Marshal(map[string]any{"matched": matched})
		return b, nil, branch, err
	case wf.NodeEnd:
		return json.RawMessage(`{}`), nil, "", nil
	default:
		return nil, nil, "", fmt.Errorf("unsupported node type %s", n.Type)
	}
}
func cachedNodeResult(n wf.Node, b json.RawMessage) (json.RawMessage, any, string, error) {
	if n.Type == wf.NodeAgent {
		var result agentdom.Result
		if err := json.Unmarshal(b, &result); err != nil {
			return nil, nil, "", err
		}
		return b, map[string]any{"content": result.Content, "status": result.Status, "task_id": result.TaskID}, "", nil
	}
	if n.Type == wf.NodeCondition {
		var x struct {
			Matched bool `json:"matched"`
		}
		if err := json.Unmarshal(b, &x); err != nil {
			return nil, nil, "", err
		}
		branch := "false"
		if x.Matched {
			branch = "true"
		}
		return b, nil, branch, nil
	}
	return b, nil, "", nil
}
func edgeIsActive(parent wf.Node, state wf.NodeState, edge wf.Edge) bool {
	if parent.Type != wf.NodeCondition {
		return true
	}
	var v struct {
		Matched bool `json:"matched"`
	}
	if json.Unmarshal(state.Output, &v) != nil {
		return false
	}
	if v.Matched {
		return edge.Handle == "true"
	}
	return edge.Handle == "false"
}
func outgoingIDs(c *compiler.Compiled, id string) []string {
	out := make([]string, 0, len(c.Outgoing[id]))
	for _, e := range c.Outgoing[id] {
		out = append(out, e.To)
	}
	return out
}
func unique(in []string) []string {
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
func removeIDs(in, remove []string) []string {
	m := map[string]bool{}
	for _, v := range remove {
		m[v] = true
	}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !m[v] {
			out = append(out, v)
		}
	}
	return out
}

func next(c *compiler.Compiled, id, handle string) string {
	for _, e := range c.Outgoing[id] {
		if handle == "" || e.Handle == handle {
			return e.To
		}
	}
	return ""
}
func lookup(m map[string]any, key string) any {
	var v any = m
	for _, p := range split(key) {
		x, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v, ok = x[p]
		if !ok {
			return nil
		}
	}
	return v
}
func split(s string) []string {
	out := []string{}
	part := ""
	for _, r := range s {
		if r == '.' {
			if part != "" {
				out = append(out, part)
				part = ""
			}
		} else {
			part += string(r)
		}
	}
	if part != "" {
		out = append(out, part)
	}
	return out
}
func render(s string, m map[string]any) string {
	b, _ := json.Marshal(m)
	return fmt.Sprintf("%s\n\nWorkflow context: %s", s, string(b))
}
func stableTaskID(runID, nodeID string) string {
	sum := sha256.Sum256([]byte(runID + ":" + nodeID))
	return "wf-" + hex.EncodeToString(sum[:16])
}
func newID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
