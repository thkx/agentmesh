package goal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/goal"
)

var ErrInvalidRequest = errors.New("invalid goal request")

type Service struct{ repo Repository }

func NewService(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, errors.New("goal repository is required")
	}
	return &Service{repo: repo}, nil
}

func (s *Service) Create(ctx context.Context, in dom.Goal) (dom.Goal, error) {
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		id, err := newID()
		if err != nil {
			return dom.Goal{}, fmt.Errorf("generate goal id: %w", err)
		}
		in.ID = id
	}
	in.Description = strings.TrimSpace(in.Description)
	if in.Status != "" && in.Status != dom.StatusActive {
		return dom.Goal{}, ErrInvalidRequest
	}
	in.Status = dom.StatusActive
	now := time.Now().UTC()
	in.CreatedAt, in.UpdatedAt = now, now
	in.CanceledAt = nil
	if in.Constraints == nil {
		in.Constraints = []string{}
	}
	if in.SuccessCriteria == nil {
		in.SuccessCriteria = []dom.SuccessCriterion{}
	}
	if err := in.Validate(); err != nil {
		return dom.Goal{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := s.repo.Create(ctx, in); err != nil {
		return dom.Goal{}, fmt.Errorf("persist goal: %w", err)
	}
	return in, nil
}

func (s *Service) Update(ctx context.Context, id string, in dom.Goal) (dom.Goal, error) {
	current, err := s.repo.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return dom.Goal{}, err
	}
	if current.Status != dom.StatusActive {
		return dom.Goal{}, dom.ErrInvalidTransition
	}
	in.ID = current.ID
	in.Status = current.Status
	in.CreatedAt = current.CreatedAt
	in.UpdatedAt = time.Now().UTC()
	in.CanceledAt = nil
	in.Description = strings.TrimSpace(in.Description)
	if in.Constraints == nil {
		in.Constraints = []string{}
	}
	if in.SuccessCriteria == nil {
		in.SuccessCriteria = []dom.SuccessCriterion{}
	}
	if err := in.Validate(); err != nil {
		return dom.Goal{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := s.repo.Update(ctx, in); err != nil {
		return dom.Goal{}, err
	}
	return in, nil
}

func (s *Service) Get(ctx context.Context, id string) (dom.Goal, error) {
	return s.repo.Get(ctx, strings.TrimSpace(id))
}

func (s *Service) List(ctx context.Context, limit int) ([]dom.Goal, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.repo.List(ctx, limit)
}

func (s *Service) Cancel(ctx context.Context, id string) (dom.Goal, error) {
	g, err := s.repo.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return dom.Goal{}, err
	}
	if g.Status == dom.StatusCanceled {
		return g, nil
	}
	if g.Status != dom.StatusActive {
		return dom.Goal{}, dom.ErrInvalidTransition
	}
	now := time.Now().UTC()
	if err := s.repo.Cancel(ctx, g.ID, now); err != nil {
		return dom.Goal{}, err
	}
	g.Status, g.UpdatedAt, g.CanceledAt = dom.StatusCanceled, now, &now
	return g, nil
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
