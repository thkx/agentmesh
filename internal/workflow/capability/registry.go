package capability

import (
	"errors"
	"fmt"
	"sync"

	"github.com/thkx/agentmesh/internal/domain/workflow"
)

var ErrNotFound = errors.New("workflow capability not found")

type Capability interface {
	Name() string
	Configure(*workflow.AgentConfig) error
}
type Registry struct {
	mu    sync.RWMutex
	items map[string]Capability
}

func NewRegistry() *Registry { return &Registry{items: map[string]Capability{}} }
func (r *Registry) Register(c Capability) error {
	if c == nil || c.Name() == "" {
		return ErrNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[c.Name()]; ok {
		return errors.New("capability already registered")
	}
	r.items[c.Name()] = c
	return nil
}
func (r *Registry) Get(name string) (Capability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.items[name]
	if !ok {
		return nil, ErrNotFound
	}
	return c, nil
}
func (r *Registry) Apply(names []string, cfg *workflow.AgentConfig) error {
	for _, name := range names {
		c, err := r.Get(name)
		if err != nil {
			return fmt.Errorf("capability %q: %w", name, err)
		}
		if err = c.Configure(cfg); err != nil {
			return fmt.Errorf("configure capability %q: %w", name, err)
		}
	}
	return nil
}

// ToolSet is a reusable capability that contributes an allowlisted tool set and prompt context.
type ToolSet struct {
	ID           string
	Tools        []string
	PromptPrefix string
}

func (c ToolSet) Name() string { return c.ID }
func (c ToolSet) Configure(cfg *workflow.AgentConfig) error {
	if cfg == nil {
		return errors.New("agent config is nil")
	}
	seen := map[string]bool{}
	for _, name := range cfg.ApprovedTools {
		seen[name] = true
	}
	for _, name := range c.Tools {
		if name == "" {
			return errors.New("tool name is empty")
		}
		if !seen[name] {
			cfg.ApprovedTools = append(cfg.ApprovedTools, name)
			seen[name] = true
		}
	}
	if c.PromptPrefix != "" {
		cfg.Prompt = c.PromptPrefix + "\n\n" + cfg.Prompt
	}
	return nil
}
