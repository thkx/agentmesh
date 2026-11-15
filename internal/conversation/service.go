package conversation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type Service struct {
	repo  Repository
	clock func() time.Time
}

func NewService(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, errors.New("conversation repository is required")
	}
	return &Service{repo: repo, clock: time.Now}, nil
}
func newID() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func (s *Service) Create(ctx context.Context, title string, scope Scope, scopeID string, meta map[string]any) (Conversation, error) {
	id, e := newID()
	if e != nil {
		return Conversation{}, e
	}
	now := s.clock().UTC()
	c := Conversation{ID: id, Title: strings.TrimSpace(title), ScopeType: scope, ScopeID: strings.TrimSpace(scopeID), Metadata: meta, CreatedAt: now, UpdatedAt: now}
	if c.ScopeType == "" {
		c.ScopeType = ScopeAgent
	}
	if e = c.Validate(); e != nil {
		return Conversation{}, e
	}
	if e = s.repo.Create(ctx, c); e != nil {
		return Conversation{}, e
	}
	return c, nil
}
func (s *Service) Append(ctx context.Context, conversationID, role, name, content, toolCallID, toolCallsJSON string) (Message, error) {
	id, e := newID()
	if e != nil {
		return Message{}, e
	}
	m := Message{ID: id, ConversationID: conversationID, Role: role, Name: name, Content: content, ToolCallID: toolCallID, ToolCallsJSON: toolCallsJSON, CreatedAt: s.clock().UTC()}
	if e = m.Validate(); e != nil {
		return Message{}, e
	}
	return s.repo.Append(ctx, m)
}
func (s *Service) Messages(ctx context.Context, id string, after int64, limit int) ([]Message, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrInvalid
	}
	if _, e := s.repo.Get(ctx, id); e != nil {
		return nil, e
	}
	return s.repo.Messages(ctx, id, after, limit)
}
func (s *Service) List(ctx context.Context, scope Scope, scopeID string, limit int) ([]Conversation, error) {
	return s.repo.List(ctx, scope, scopeID, limit)
}
func (s *Service) Archive(ctx context.Context, id string) error {
	return s.repo.Archive(ctx, id, s.clock().UTC())
}
func (s *Service) Repository() Repository { return s.repo }
