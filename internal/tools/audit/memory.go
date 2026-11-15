package audit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/thkx/agentmesh/internal/tools"
)

type Memory struct {
	mu        sync.RWMutex
	limit     int
	events    []tools.AuditEvent
	approvals map[string]memoryApproval
}

func NewMemory(limit int) *Memory {
	if limit < 1 {
		limit = 1000
	}
	return &Memory{limit: limit, approvals: make(map[string]memoryApproval)}
}
func (m *Memory) Record(_ context.Context, e tools.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
	if len(m.events) > m.limit {
		m.events = append([]tools.AuditEvent(nil), m.events[len(m.events)-m.limit:]...)
	}
	return nil
}
func (m *Memory) List(limit int) []tools.AuditEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 || limit > len(m.events) {
		limit = len(m.events)
	}
	out := append([]tools.AuditEvent(nil), m.events[len(m.events)-limit:]...)
	return out
}

type memoryApproval struct {
	tool, workspace, argsHash string
	expires                   time.Time
	used                      bool
}

func (m *Memory) CreateApproval(_ context.Context, hash, tool, workspace, argsHash string, expires time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.approvals[hash]; ok {
		return fmt.Errorf("approval already exists")
	}
	m.approvals[hash] = memoryApproval{tool: tool, workspace: workspace, argsHash: argsHash, expires: expires}
	return nil
}
func (m *Memory) ConsumeApproval(_ context.Context, hash, tool, workspace, argsHash string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.approvals[hash]
	if !ok || a.used || !now.Before(a.expires) || a.tool != tool || a.workspace != workspace || a.argsHash != argsHash {
		return false, nil
	}
	a.used = true
	m.approvals[hash] = a
	return true, nil
}

var _ tools.ApprovalStore = (*Memory)(nil)
