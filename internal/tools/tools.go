package tools

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/tool"
)

type Handler interface {
	Invoke(context.Context, json.RawMessage) (any, error)
}
type HandlerFunc func(context.Context, json.RawMessage) (any, error)

func (f HandlerFunc) Invoke(ctx context.Context, in json.RawMessage) (any, error) { return f(ctx, in) }

type Tool struct {
	Spec    dom.Spec
	Handler Handler
}
type Registry struct {
	mu    sync.RWMutex
	items map[string]Tool
}

func NewRegistry() *Registry { return &Registry{items: make(map[string]Tool)} }
func (r *Registry) Register(t Tool) error {
	if err := t.Spec.Validate(); err != nil {
		return err
	}
	if t.Handler == nil {
		return dom.ErrInvalidSpec
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[t.Spec.Name]; ok {
		return fmt.Errorf("tool %q already registered", t.Spec.Name)
	}
	r.items[t.Spec.Name] = t
	return nil
}
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.items[name]
	if !ok {
		return Tool{}, dom.ErrNotFound
	}
	return t, nil
}
func (r *Registry) List() []dom.Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]dom.Spec, 0, len(r.items))
	for _, t := range r.items {
		out = append(out, t.Spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type Decision struct {
	Allowed          bool
	RequiresApproval bool
	Reason           string
}
type Policy interface {
	Authorize(context.Context, dom.Spec, json.RawMessage, string) Decision
}
type Invoker interface {
	Invoke(context.Context, string, json.RawMessage, string, bool) (any, error)
}

// ApprovalStore persists single-use approvals. The raw approval token is never stored.
type ApprovalStore interface {
	CreateApproval(context.Context, string, string, string, string, time.Time) error
	ConsumeApproval(context.Context, string, string, string, string, time.Time) (bool, error)
}

type agentGrantKey struct{}
type agentInvocationKey struct{}

// WithAgentInvocation marks calls originating from Agent Runtime so the Engine
// can enforce the server-loaded ApprovedTools list independently of the prompt.
func WithAgentInvocation(ctx context.Context) context.Context {
	return context.WithValue(ctx, agentInvocationKey{}, true)
}
func isAgentInvocation(ctx context.Context) bool {
	v, _ := ctx.Value(agentInvocationKey{}).(bool)
	return v
}

// WithAgentToolGrant marks a tool grant created by the trusted Agent Runtime from
// a server-loaded Agent Definition. HTTP clients cannot manufacture this context.
func WithAgentToolGrant(ctx context.Context, tool string) context.Context {
	return context.WithValue(ctx, agentGrantKey{}, tool)
}
func hasAgentToolGrant(ctx context.Context, tool string) bool {
	v, _ := ctx.Value(agentGrantKey{}).(string)
	return v != "" && v == tool
}

type AuditEvent struct {
	ID            string        `json:"id"`
	Tool          string        `json:"tool"`
	Workspace     string        `json:"workspace"`
	Allowed       bool          `json:"allowed"`
	Approved      bool          `json:"approved"`
	Authorization string        `json:"authorization,omitempty"`
	StartedAt     time.Time     `json:"started_at"`
	Duration      time.Duration `json:"duration"`
	Error         string        `json:"error,omitempty"`
}
type Auditor interface {
	Record(context.Context, AuditEvent) error
}
type Engine struct {
	registry            *Registry
	policy              Policy
	auditor             Auditor
	approvals           ApprovalStore
	maxInput, maxOutput int
}
type workspaceContextKey struct{}

func WithWorkspace(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, workspaceContextKey{}, path)
}
func WorkspaceFromContext(ctx context.Context) string {
	v, _ := ctx.Value(workspaceContextKey{}).(string)
	return v
}
func NewEngine(r *Registry, p Policy, a Auditor) *Engine {
	return &Engine{registry: r, policy: p, auditor: a, maxInput: 1 << 20, maxOutput: 4 << 20}
}
func (e *Engine) Registry() *Registry                  { return e.registry }
func (e *Engine) SetApprovalStore(store ApprovalStore) { e.approvals = store }

// IssueApproval creates a short-lived, single-use authorization for one exact
// tool invocation. It is bound to the tool, workspace and canonical arguments.
func (e *Engine) IssueApproval(ctx context.Context, name string, args json.RawMessage, workspace string, ttl time.Duration) (string, time.Time, error) {
	if e.approvals == nil {
		return "", time.Time{}, fmt.Errorf("approval store is not configured")
	}
	t, err := e.registry.Get(name)
	if err != nil {
		return "", time.Time{}, err
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if len(args) > e.maxInput {
		return "", time.Time{}, dom.ErrInvalidArguments
	}
	if err = validateArgs(t.Spec.Input, args); err != nil {
		return "", time.Time{}, err
	}
	d := e.policy.Authorize(ctx, t.Spec, args, workspace)
	if !d.RequiresApproval {
		return "", time.Time{}, fmt.Errorf("tool does not require a human approval")
	}
	if ttl <= 0 || ttl > 5*time.Minute {
		ttl = 2 * time.Minute
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	expires := time.Now().UTC().Add(ttl)
	if err = e.approvals.CreateApproval(ctx, hashToken(token), name, workspace, hashArguments(args), expires); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// Invoke retains the old signature for internal callers, but the bool is
// intentionally ignored. Only a trusted Agent Runtime context or a persisted
// one-time approval can satisfy an approval-required policy decision.
func (e *Engine) Invoke(ctx context.Context, name string, args json.RawMessage, workspace string, _ bool) (any, error) {
	return e.invoke(ctx, name, args, workspace, "")
}

func (e *Engine) InvokeWithApproval(ctx context.Context, name string, args json.RawMessage, workspace, approvalID string) (any, error) {
	return e.invoke(ctx, name, args, workspace, approvalID)
}

func (e *Engine) invoke(ctx context.Context, name string, args json.RawMessage, workspace, approvalID string) (value any, retErr error) {
	started := time.Now()
	ev := AuditEvent{ID: fmt.Sprintf("tool-%d", started.UnixNano()), Tool: name, Workspace: workspace, StartedAt: started}
	defer func() {
		ev.Duration = time.Since(started)
		if e.auditor != nil {
			if auditErr := e.auditor.Record(context.Background(), ev); auditErr != nil && retErr == nil {
				retErr = fmt.Errorf("persist tool audit event: %w", auditErr)
			}
		}
	}()
	t, err := e.registry.Get(name)
	if err != nil {
		ev.Error = err.Error()
		return nil, err
	}
	if len(args) > e.maxInput {
		ev.Error = dom.ErrInvalidArguments.Error()
		return nil, dom.ErrInvalidArguments
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if err = validateArgs(t.Spec.Input, args); err != nil {
		ev.Error = err.Error()
		return nil, err
	}
	d := e.policy.Authorize(ctx, t.Spec, args, workspace)
	if isAgentInvocation(ctx) && t.Spec.Permission != dom.PermissionRead && !hasAgentToolGrant(ctx, name) {
		ev.Error = dom.ErrDenied.Error()
		return nil, dom.ErrDenied
	}
	if d.RequiresApproval {
		switch {
		case hasAgentToolGrant(ctx, name):
			ev.Approved = true
			ev.Authorization = "agent_capability"
		case approvalID != "" && e.approvals != nil:
			ok, consumeErr := e.approvals.ConsumeApproval(ctx, hashToken(approvalID), name, workspace, hashArguments(args), time.Now().UTC())
			if consumeErr != nil {
				ev.Error = consumeErr.Error()
				return nil, consumeErr
			}
			if !ok {
				ev.Error = dom.ErrApprovalRequired.Error()
				return nil, dom.ErrApprovalRequired
			}
			ev.Approved = true
			ev.Authorization = "human_approval"
		default:
			ev.Error = dom.ErrApprovalRequired.Error()
			return nil, dom.ErrApprovalRequired
		}
	} else if !d.Allowed {
		ev.Error = dom.ErrDenied.Error()
		return nil, dom.ErrDenied
	}
	ev.Allowed = true
	callCtx := WithWorkspace(ctx, workspace)
	cancel := func() {}
	if t.Spec.Timeout > 0 {
		callCtx, cancel = context.WithTimeout(callCtx, t.Spec.Timeout)
	}
	defer cancel()
	result, err := t.Handler.Invoke(callCtx, args)
	if err != nil {
		ev.Error = err.Error()
		return nil, err
	}
	b, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return nil, marshalErr
	}
	if len(b) > e.maxOutput {
		ev.Error = dom.ErrOutputTooLarge.Error()
		return nil, dom.ErrOutputTooLarge
	}
	return result, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func hashArguments(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) == nil {
		if canonical, err := json.Marshal(value); err == nil {
			raw = canonical
		}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validateArgs(s dom.Schema, raw json.RawMessage) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return dom.ErrInvalidArguments
	}
	if !s.AdditionalProperties {
		for k := range m {
			if _, ok := s.Properties[k]; !ok {
				return fmt.Errorf("%w: unknown field %q", dom.ErrInvalidArguments, k)
			}
		}
	}
	for _, k := range s.Required {
		if _, ok := m[k]; !ok {
			return fmt.Errorf("%w: missing field %q", dom.ErrInvalidArguments, k)
		}
	}
	for k, v := range m {
		p, ok := s.Properties[k]
		if !ok {
			continue
		}
		var value any
		if json.Unmarshal(v, &value) != nil {
			return dom.ErrInvalidArguments
		}
		valid := false
		switch p.Type {
		case "string":
			_, valid = value.(string)
		case "boolean":
			_, valid = value.(bool)
		case "object":
			_, valid = value.(map[string]any)
		case "array":
			_, valid = value.([]any)
		case "number":
			_, valid = value.(float64)
		case "integer":
			f, ok := value.(float64)
			valid = ok && f == float64(int64(f))
		}
		if !valid {
			return fmt.Errorf("%w: field %q must be %s", dom.ErrInvalidArguments, k, p.Type)
		}
		if len(p.Enum) > 0 {
			str, ok := value.(string)
			if !ok {
				return dom.ErrInvalidArguments
			}
			found := false
			for _, x := range p.Enum {
				if str == x {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("%w: invalid value for %q", dom.ErrInvalidArguments, k)
			}
		}
	}
	return nil
}
