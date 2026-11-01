package tools

import (
	"context"
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
type AuditEvent struct {
	ID        string        `json:"id"`
	Tool      string        `json:"tool"`
	Workspace string        `json:"workspace"`
	Allowed   bool          `json:"allowed"`
	Approved  bool          `json:"approved"`
	StartedAt time.Time     `json:"started_at"`
	Duration  time.Duration `json:"duration"`
	Error     string        `json:"error,omitempty"`
}
type Auditor interface {
	Record(context.Context, AuditEvent) error
}
type Engine struct {
	registry            *Registry
	policy              Policy
	auditor             Auditor
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
func (e *Engine) Registry() *Registry { return e.registry }
func (e *Engine) Invoke(ctx context.Context, name string, args json.RawMessage, workspace string, approved bool) (any, error) {
	started := time.Now()
	ev := AuditEvent{ID: fmt.Sprintf("tool-%d", started.UnixNano()), Tool: name, Workspace: workspace, Approved: approved, StartedAt: started}
	defer func() {
		ev.Duration = time.Since(started)
		if e.auditor != nil {
			_ = e.auditor.Record(context.Background(), ev)
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
	if d.RequiresApproval && !approved {
		ev.Allowed = false
		ev.Error = dom.ErrApprovalRequired.Error()
		return nil, dom.ErrApprovalRequired
	}
	if !d.Allowed && !d.RequiresApproval {
		ev.Allowed = false
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
