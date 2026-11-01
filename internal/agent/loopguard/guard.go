package loopguard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	dom "github.com/thkx/agentmesh/internal/domain/agent"
)

type Guard struct {
	maxSteps, maxCalls int
	seen               map[string]int
}

func New(maxSteps, maxCalls int) *Guard {
	if maxSteps <= 0 {
		maxSteps = 12
	}
	if maxCalls <= 0 {
		maxCalls = 8
	}
	return &Guard{maxSteps: maxSteps, maxCalls: maxCalls, seen: make(map[string]int)}
}
func (g *Guard) CheckStep(step int) error {
	if step > g.maxSteps {
		return fmt.Errorf("%w: limit=%d", dom.ErrMaxSteps, g.maxSteps)
	}
	return nil
}
func (g *Guard) CheckToolCall(calls int, name string, args any) error {
	if calls >= g.maxCalls {
		return fmt.Errorf("%w: limit=%d", dom.ErrMaxToolCalls, g.maxCalls)
	}
	b, _ := json.Marshal(struct {
		Name string `json:"name"`
		Args any    `json:"args"`
	}{name, args})
	sum := sha256.Sum256(b)
	key := hex.EncodeToString(sum[:])
	if g.seen[key] >= 2 {
		return fmt.Errorf("%w: %s", dom.ErrRepeatedToolCall, name)
	}
	g.seen[key]++
	return nil
}
