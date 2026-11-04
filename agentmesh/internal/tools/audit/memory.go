package audit

import (
	"context"
	"github.com/thkx/agentmesh/internal/tools"
	"sync"
)

type Memory struct {
	mu     sync.RWMutex
	limit  int
	events []tools.AuditEvent
}

func NewMemory(limit int) *Memory {
	if limit < 1 {
		limit = 1000
	}
	return &Memory{limit: limit}
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
