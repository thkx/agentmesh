package airesource

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

type Router struct {
	service  *Service
	mu       sync.Mutex
	cursor   map[string]uint64
	inflight map[string]int
	halfOpen map[string]bool
}

func NewRouter(s *Service) (*Router, error) {
	if s == nil {
		return nil, errors.New("resource service is nil")
	}
	return &Router{service: s, cursor: map[string]uint64{}, inflight: map[string]int{}, halfOpen: map[string]bool{}}, nil
}
func (r *Router) acquire(c Candidate) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inflight[c.Credential.ID] >= c.Credential.MaxConcurrency {
		return false
	}
	if (c.Health.State == "open" || c.Health.State == "half_open") && !c.Health.CooldownUntil.After(time.Now()) {
		if r.halfOpen[c.Credential.ID] {
			return false
		}
		r.halfOpen[c.Credential.ID] = true
	}
	r.inflight[c.Credential.ID]++
	return true
}
func (r *Router) release(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inflight[id] > 0 {
		r.inflight[id]--
	}
	delete(r.halfOpen, id)
}
func (r *Router) inFlight(id string) int { r.mu.Lock(); defer r.mu.Unlock(); return r.inflight[id] }
func (r *Router) choose(modelID string, candidates []Candidate, policy RoutingPolicy, excluded map[string]bool) (Candidate, bool) {
	now := time.Now()
	usable := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if excluded[c.Credential.ID] {
			continue
		}
		if c.Health.CooldownUntil.After(now) {
			continue
		}
		if c.Health.State == "open" && c.Health.CooldownUntil.IsZero() {
			continue
		}
		usable = append(usable, c)
	}
	if len(usable) == 0 {
		return Candidate{}, false
	}
	sort.SliceStable(usable, func(i, j int) bool {
		if usable[i].Credential.Priority != usable[j].Credential.Priority {
			return usable[i].Credential.Priority < usable[j].Credential.Priority
		}
		return usable[i].Endpoint.Priority < usable[j].Endpoint.Priority
	})
	switch policy.Strategy {
	case "priority":
		for _, c := range usable {
			if r.acquire(c) {
				return c, true
			}
		}
	case "least_connections":
		sort.SliceStable(usable, func(i, j int) bool { return r.inFlight(usable[i].Credential.ID) < r.inFlight(usable[j].Credential.ID) })
		for _, c := range usable {
			if r.acquire(c) {
				return c, true
			}
		}
	case "round_robin", "weighted_round_robin":
		r.mu.Lock()
		start := r.cursor[modelID]
		r.cursor[modelID] = start + 1
		r.mu.Unlock()
		pool := usable
		if policy.Strategy == "weighted_round_robin" {
			pool = make([]Candidate, 0)
			for _, c := range usable {
				w := c.Credential.Weight
				if w > 100 {
					w = 100
				}
				for i := 0; i < w; i++ {
					pool = append(pool, c)
				}
			}
		}
		for i := 0; i < len(pool); i++ {
			c := pool[(int(start)+i)%len(pool)]
			if r.acquire(c) {
				return c, true
			}
		}
	default:
		for _, c := range usable {
			if r.acquire(c) {
				return c, true
			}
		}
	}
	return Candidate{}, false
}
func statusOf(err error) int {
	var ue *upstreamError
	if errors.As(err, &ue) {
		return ue.Status
	}
	return 0
}
func retryable(err error) bool {
	if err == nil {
		return false
	}
	code := statusOf(err)
	return code == 0 || code == 401 || code == 403 || code == 408 || code == 409 || code == 425 || code == 429 || code >= 500
}
func (r *Router) Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	if req.Model == "" || len(req.Messages) == 0 {
		return GenerateResponse{}, fmt.Errorf("model and messages are required")
	}
	if req.Stream {
		return GenerateResponse{}, ErrStreamingUnsupported
	}
	candidates, policy, err := r.service.Candidates(ctx, req.Model)
	if err != nil {
		return GenerateResponse{}, err
	}
	if len(req.Tools) > 0 {
		compatible := make([]Candidate, 0, len(candidates))
		for _, candidate := range candidates {
			if candidate.Provider.Protocol == ProtocolOpenAI {
				compatible = append(compatible, candidate)
			}
		}
		if len(compatible) == 0 {
			return GenerateResponse{}, ErrNativeToolCallingUnsupported
		}
		candidates = compatible
	}
	if len(candidates) == 0 {
		return GenerateResponse{}, ErrNoCandidate
	}
	maxAttempts := policy.MaxRetries + 1
	if maxAttempts > len(candidates) {
		maxAttempts = len(candidates)
	}
	excluded := map[string]bool{}
	var last error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if e := ctx.Err(); e != nil {
			return GenerateResponse{}, e
		}
		candidate, ok := r.choose(req.Model, candidates, policy, excluded)
		if !ok {
			break
		}
		excluded[candidate.Credential.ID] = true
		started := time.Now()
		resp, status, callErr := generate(ctx, candidate, req)
		latency := time.Since(started)
		recordCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		recordErr := r.service.RecordResult(recordCtx, candidate, newID("req"), status, latency, callErr, resp.Usage, policy)
		cancel()
		r.release(candidate.Credential.ID)
		if recordErr != nil && callErr == nil {
			return GenerateResponse{}, fmt.Errorf("record gateway usage: %w", recordErr)
		}
		if callErr == nil {
			return resp, nil
		}
		last = callErr
		if !retryable(callErr) {
			break
		}
	}
	if last != nil {
		return GenerateResponse{}, fmt.Errorf("all eligible credentials failed: %w", last)
	}
	return GenerateResponse{}, ErrNoCandidate
}
