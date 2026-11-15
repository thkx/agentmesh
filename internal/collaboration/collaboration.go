package collaboration

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"strings"
	"time"

	"github.com/thkx/agentmesh/internal/conversation"
)

var (
	ErrInvalid                   = errors.New("invalid collaboration request")
	ErrNotFound                  = errors.New("collaboration resource not found")
	ErrConflict                  = errors.New("collaboration state conflict")
	ErrLeaseLost                 = errors.New("assignment lease lost")
	ErrWorkspaceConcurrencyLimit = errors.New("workspace agent concurrency limit reached")
	ErrAgentBusy                 = errors.New("agent already has an active assignment")
	ErrCapabilityMismatch        = errors.New("agent does not satisfy task capability requirements")
	ErrApprovalRequired          = errors.New("human approval required")
)

type Workspace struct {
	ID                  string    `json:"id,omitempty"`
	Name                string    `json:"name,omitempty"`
	Slug                string    `json:"slug,omitempty"`
	Description         string    `json:"description,omitempty"`
	AutonomyMode        string    `json:"autonomy_mode,omitempty"`
	MaxAgentConcurrency int       `json:"max_agent_concurrency,omitempty"`
	MaxDelegationDepth  int       `json:"max_delegation_depth,omitempty"`
	CreatedAt           time.Time `json:"created_at,omitempty"`
	UpdatedAt           time.Time `json:"updated_at,omitempty"`
}
type AgentIdentity struct {
	ID           string    `json:"id,omitempty"`
	WorkspaceID  string    `json:"workspace_id,omitempty"`
	DefinitionID string    `json:"definition_id,omitempty"`
	Name         string    `json:"name,omitempty"`
	Description  string    `json:"description,omitempty"`
	Role         string    `json:"role,omitempty"`
	Status       string    `json:"status,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}
type Channel struct {
	ID             string    `json:"id,omitempty"`
	WorkspaceID    string    `json:"workspace_id,omitempty"`
	Name           string    `json:"name,omitempty"`
	Kind           string    `json:"kind,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	CreatedBy      string    `json:"created_by,omitempty"`
	MemberIDs      []string  `json:"member_ids,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
}
type Thread struct {
	ID            string    `json:"id,omitempty"`
	ChannelID     string    `json:"channel_id,omitempty"`
	Title         string    `json:"title,omitempty"`
	RootMessageID string    `json:"root_message_id,omitempty"`
	CreatedBy     string    `json:"created_by,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
}
type Task struct {
	ID                   string    `json:"id,omitempty"`
	WorkspaceID          string    `json:"workspace_id,omitempty"`
	ParentTaskID         string    `json:"parent_task_id,omitempty"`
	Title                string    `json:"title,omitempty"`
	Description          string    `json:"description,omitempty"`
	Type                 string    `json:"type,omitempty"`
	Status               string    `json:"status,omitempty"`
	Priority             int       `json:"priority,omitempty"`
	CreatedBy            string    `json:"created_by,omitempty"`
	IdempotencyKey       string    `json:"idempotency_key,omitempty"`
	DelegationDepth      int       `json:"delegation_depth,omitempty"`
	RequiredCapabilities []string  `json:"required_capabilities,omitempty"`
	CreatedAt            time.Time `json:"created_at,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
}
type Assignment struct {
	ID             string     `json:"id,omitempty"`
	TaskID         string     `json:"task_id,omitempty"`
	AgentID        string     `json:"agent_id,omitempty"`
	Status         string     `json:"status,omitempty"`
	AssignedBy     string     `json:"assigned_by,omitempty"`
	LeaseOwner     string     `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	ClaimVersion   int64      `json:"claim_version,omitempty"`
	CreatedAt      time.Time  `json:"created_at,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at,omitempty"`
}

type Handoff struct {
	RequesterAgentID     string   `json:"requester_agent_id"`
	Goal                 string   `json:"goal"`
	Completed            []string `json:"completed"`
	Remaining            []string `json:"remaining"`
	Artifacts            []string `json:"artifacts"`
	Constraints          []string `json:"constraints"`
	AcceptanceCriteria   []string `json:"acceptance_criteria"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	Reason               string   `json:"reason"`
	Recipient            string   `json:"recipient"`
	IdempotencyKey       string   `json:"idempotency_key"`
}

type Event struct {
	ID            string          `json:"id,omitempty"`
	WorkspaceID   string          `json:"workspace_id,omitempty"`
	AggregateType string          `json:"aggregate_type,omitempty"`
	AggregateID   string          `json:"aggregate_id,omitempty"`
	Type          string          `json:"type,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	Attempts      int             `json:"attempts,omitempty"`
	LastError     string          `json:"last_error,omitempty"`
	CreatedAt     time.Time       `json:"created_at,omitempty"`
	PublishedAt   *time.Time      `json:"published_at,omitempty"`
}

type ApprovalRequiredError struct{ ApprovalID string }

func (e *ApprovalRequiredError) Error() string { return ErrApprovalRequired.Error() }

type Service struct {
	db            *sql.DB
	conversations *conversation.Service
	clock         func() time.Time
}

func NewService(db *sql.DB, cs *conversation.Service) (*Service, error) {
	if db == nil || cs == nil {
		return nil, errors.New("collaboration dependencies required")
	}
	return &Service{db: db, conversations: cs, clock: time.Now}, nil
}
func newID() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}

func (s *Service) writeEventTx(ctx context.Context, tx *sql.Tx, workspace, aggregateType, aggregateID, eventType string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	eid, err := newID()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO collaboration_outbox(id,workspace_id,aggregate_type,aggregate_id,event_type,payload_json,created_at) VALUES(?,?,?,?,?,?,?)", eid, workspace, aggregateType, aggregateID, eventType, string(b), s.clock().UTC().UnixMilli())
	return err
}

func (s *Service) emit(ctx context.Context, workspace, aggregateType, aggregateID, eventType string, payload any) error {
	b, e := json.Marshal(payload)
	if e != nil {
		return e
	}
	id, e := newID()
	if e != nil {
		return e
	}
	_, e = s.db.ExecContext(ctx, "INSERT INTO collaboration_outbox(id,workspace_id,aggregate_type,aggregate_id,event_type,payload_json,created_at) VALUES(?,?,?,?,?,?,?)", id, workspace, aggregateType, aggregateID, eventType, string(b), s.clock().UTC().UnixMilli())
	return e
}

func (s *Service) CreateWorkspace(ctx context.Context, v Workspace) (Workspace, error) {
	v.Name = strings.TrimSpace(v.Name)
	v.Slug = strings.ToLower(strings.TrimSpace(v.Slug))
	if v.ID == "" {
		v.ID, _ = newID()
	}
	if v.AutonomyMode == "" {
		v.AutonomyMode = "hybrid"
	}
	if v.MaxAgentConcurrency == 0 {
		v.MaxAgentConcurrency = 4
	}
	if v.MaxDelegationDepth == 0 {
		v.MaxDelegationDepth = 3
	}
	if v.Name == "" || v.Slug == "" || len(v.Name) > 160 || len(v.Slug) > 100 || v.MaxAgentConcurrency < 1 || v.MaxAgentConcurrency > 256 || v.MaxDelegationDepth < 0 || v.MaxDelegationDepth > 32 {
		return v, ErrInvalid
	}
	if v.AutonomyMode != "directed" && v.AutonomyMode != "hybrid" && v.AutonomyMode != "autonomous" {
		return v, ErrInvalid
	}
	now := s.clock().UTC()
	v.CreatedAt = now
	v.UpdatedAt = now
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, "INSERT INTO collaboration_workspaces(id,name,slug,description,autonomy_mode,max_agent_concurrency,max_delegation_depth,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)", v.ID, v.Name, v.Slug, v.Description, v.AutonomyMode, v.MaxAgentConcurrency, v.MaxDelegationDepth, now.UnixMilli(), now.UnixMilli())
	if e != nil {
		return v, e
	}
	b, _ := json.Marshal(v)
	eid, _ := newID()
	_, e = tx.ExecContext(ctx, "INSERT INTO collaboration_outbox(id,workspace_id,aggregate_type,aggregate_id,event_type,payload_json,created_at) VALUES(?,?,?,?,?,?,?)", eid, v.ID, "workspace", v.ID, "workspace.created", string(b), now.UnixMilli())
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (s *Service) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT id,name,slug,description,autonomy_mode,max_agent_concurrency,max_delegation_depth,created_at,updated_at FROM collaboration_workspaces ORDER BY created_at")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		var v Workspace
		var c, u int64
		if e = rows.Scan(&v.ID, &v.Name, &v.Slug, &v.Description, &v.AutonomyMode, &v.MaxAgentConcurrency, &v.MaxDelegationDepth, &c, &u); e != nil {
			return nil, e
		}
		v.CreatedAt = time.UnixMilli(c).UTC()
		v.UpdatedAt = time.UnixMilli(u).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) GetWorkspace(ctx context.Context, id string) (Workspace, error) {
	var v Workspace
	var c, u int64
	e := s.db.QueryRowContext(ctx, "SELECT id,name,slug,description,autonomy_mode,max_agent_concurrency,max_delegation_depth,created_at,updated_at FROM collaboration_workspaces WHERE id=?", id).Scan(&v.ID, &v.Name, &v.Slug, &v.Description, &v.AutonomyMode, &v.MaxAgentConcurrency, &v.MaxDelegationDepth, &c, &u)
	if errors.Is(e, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if e != nil {
		return v, e
	}
	v.CreatedAt = time.UnixMilli(c).UTC()
	v.UpdatedAt = time.UnixMilli(u).UTC()
	return v, nil
}

func (s *Service) CreateAgent(ctx context.Context, v AgentIdentity) (AgentIdentity, error) {
	if v.ID == "" {
		var err error
		v.ID, err = newID()
		if err != nil {
			return v, err
		}
	}
	if v.Role == "" {
		v.Role = "worker"
	}
	if v.Status == "" {
		v.Status = "offline"
	}
	v.Name = strings.TrimSpace(v.Name)
	if v.WorkspaceID == "" || v.Name == "" || len(v.Name) > 160 || len(v.Capabilities) > 128 {
		return v, ErrInvalid
	}
	normalizedCapabilities, capabilityErr := normalizeCapabilityList(v.Capabilities, 128)
	if capabilityErr != nil {
		return v, capabilityErr
	}
	v.Capabilities = normalizedCapabilities
	if _, err := s.GetWorkspace(ctx, v.WorkspaceID); err != nil {
		return v, err
	}
	now := s.clock().UTC()
	v.CreatedAt = now
	v.UpdatedAt = now
	b, err := json.Marshal(v.Capabilities)
	if err != nil {
		return v, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "INSERT INTO agent_identities(id,workspace_id,definition_id,name,description,role,status,capabilities_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)", v.ID, v.WorkspaceID, null(v.DefinitionID), v.Name, v.Description, v.Role, v.Status, string(b), now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return v, err
	}
	if err = s.writeEventTx(ctx, tx, v.WorkspaceID, "agent_identity", v.ID, "agent.created", v); err != nil {
		return v, err
	}
	return v, tx.Commit()
}
func (s *Service) GetAgent(ctx context.Context, id string) (AgentIdentity, error) {
	var v AgentIdentity
	var d sql.NullString
	var caps string
	var c, u int64
	e := s.db.QueryRowContext(ctx, "SELECT id,workspace_id,definition_id,name,description,role,status,capabilities_json,created_at,updated_at FROM agent_identities WHERE id=?", id).Scan(&v.ID, &v.WorkspaceID, &d, &v.Name, &v.Description, &v.Role, &v.Status, &caps, &c, &u)
	if errors.Is(e, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if e != nil {
		return v, e
	}
	if d.Valid {
		v.DefinitionID = d.String
	}
	if e = json.Unmarshal([]byte(caps), &v.Capabilities); e != nil {
		return v, e
	}
	v.CreatedAt = time.UnixMilli(c).UTC()
	v.UpdatedAt = time.UnixMilli(u).UTC()
	return v, nil
}
func (s *Service) ListAgents(ctx context.Context, wid string) ([]AgentIdentity, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT id FROM agent_identities WHERE workspace_id=? ORDER BY created_at", wid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	out := make([]AgentIdentity, 0, len(ids))
	for _, id := range ids {
		v, e := s.GetAgent(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) CreateChannel(ctx context.Context, v Channel) (Channel, error) {
	if v.ID == "" {
		v.ID, _ = newID()
	}
	v.Name = strings.TrimSpace(v.Name)
	if v.WorkspaceID == "" || v.Name == "" || (v.Kind != "team" && v.Kind != "project" && v.Kind != "direct") {
		return v, ErrInvalid
	}
	if _, e := s.GetWorkspace(ctx, v.WorkspaceID); e != nil {
		return v, e
	}
	for _, id := range v.MemberIDs {
		a, e := s.GetAgent(ctx, id)
		if e != nil {
			return v, e
		}
		if a.WorkspaceID != v.WorkspaceID {
			return v, ErrInvalid
		}
	}
	conv, e := s.conversations.Create(ctx, v.Name, conversation.ScopeGlobal, "", map[string]any{"channel_id": v.ID, "workspace_id": v.WorkspaceID})
	if e != nil {
		return v, e
	}
	v.ConversationID = conv.ID
	v.CreatedAt = s.clock().UTC()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, "INSERT INTO collaboration_channels(id,workspace_id,name,kind,conversation_id,created_by,created_at) VALUES(?,?,?,?,?,?,?)", v.ID, v.WorkspaceID, v.Name, v.Kind, v.ConversationID, v.CreatedBy, v.CreatedAt.UnixMilli())
	if e != nil {
		return v, e
	}
	for _, aid := range v.MemberIDs {
		if _, e = tx.ExecContext(ctx, "INSERT INTO collaboration_channel_members(channel_id,agent_id,joined_at) VALUES(?,?,?)", v.ID, aid, v.CreatedAt.UnixMilli()); e != nil {
			return v, e
		}
	}
	b, _ := json.Marshal(v)
	eid, _ := newID()
	_, e = tx.ExecContext(ctx, "INSERT INTO collaboration_outbox(id,workspace_id,aggregate_type,aggregate_id,event_type,payload_json,created_at) VALUES(?,?,?,?,?,?,?)", eid, v.WorkspaceID, "channel", v.ID, "channel.created", string(b), v.CreatedAt.UnixMilli())
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (s *Service) GetChannel(ctx context.Context, id string) (Channel, error) {
	var v Channel
	var t int64
	e := s.db.QueryRowContext(ctx, "SELECT id,workspace_id,name,kind,conversation_id,created_by,created_at FROM collaboration_channels WHERE id=? AND archived_at IS NULL", id).Scan(&v.ID, &v.WorkspaceID, &v.Name, &v.Kind, &v.ConversationID, &v.CreatedBy, &t)
	if errors.Is(e, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if e != nil {
		return v, e
	}
	v.CreatedAt = time.UnixMilli(t).UTC()
	rows, e := s.db.QueryContext(ctx, "SELECT agent_id FROM collaboration_channel_members WHERE channel_id=? ORDER BY joined_at", id)
	if e != nil {
		return v, e
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if e = rows.Scan(&a); e != nil {
			return v, e
		}
		v.MemberIDs = append(v.MemberIDs, a)
	}
	return v, rows.Err()
}
func (s *Service) ListChannels(ctx context.Context, wid string) ([]Channel, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT id FROM collaboration_channels WHERE workspace_id=? AND archived_at IS NULL ORDER BY created_at DESC", wid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	out := make([]Channel, 0, len(ids))
	for _, id := range ids {
		v, e := s.GetChannel(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Service) PostMessage(ctx context.Context, cid, role, name, content string) (conversation.Message, error) {
	ch, err := s.GetChannel(ctx, cid)
	if err != nil {
		return conversation.Message{}, err
	}
	id, err := newID()
	if err != nil {
		return conversation.Message{}, err
	}
	now := s.clock().UTC()
	message := conversation.Message{ID: id, ConversationID: ch.ConversationID, Role: role, Name: name, Content: content, CreatedAt: now}
	if err = message.Validate(); err != nil {
		return conversation.Message{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return conversation.Message{}, err
	}
	defer tx.Rollback()
	var archived int64
	if err = tx.QueryRowContext(ctx, "SELECT archived_at FROM conversations WHERE id=?", ch.ConversationID).Scan(&archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return conversation.Message{}, conversation.ErrNotFound
		}
		return conversation.Message{}, err
	}
	if archived != 0 {
		return conversation.Message{}, errors.New("conversation is archived")
	}
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0)+1 FROM conversation_messages WHERE conversation_id=?", ch.ConversationID).Scan(&message.Sequence); err != nil {
		return conversation.Message{}, err
	}
	message.TokenEstimate = (len([]rune(message.Content)) + 3) / 4
	metadata, err := json.Marshal(message.Metadata)
	if err != nil {
		return conversation.Message{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO conversation_messages
		(id,conversation_id,sequence,role,name,content,tool_calls_json,tool_call_id,token_estimate,metadata_json,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, message.ID, message.ConversationID, message.Sequence, message.Role, message.Name,
		message.Content, message.ToolCallsJSON, message.ToolCallID, message.TokenEstimate, string(metadata), now.UnixMilli())
	if err != nil {
		return conversation.Message{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE conversations SET updated_at=? WHERE id=?", now.UnixMilli(), ch.ConversationID); err != nil {
		return conversation.Message{}, err
	}
	if err = s.writeEventTx(ctx, tx, ch.WorkspaceID, "channel", cid, "message.created",
		map[string]any{"channel_id": cid, "message_id": message.ID, "sequence": message.Sequence}); err != nil {
		return conversation.Message{}, err
	}
	if err = tx.Commit(); err != nil {
		return conversation.Message{}, err
	}
	return message, nil
}
func (s *Service) Messages(ctx context.Context, cid string, after int64, limit int) ([]conversation.Message, error) {
	ch, e := s.GetChannel(ctx, cid)
	if e != nil {
		return nil, e
	}
	return s.conversations.Messages(ctx, ch.ConversationID, after, limit)
}
func (s *Service) CreateThread(ctx context.Context, v Thread) (Thread, error) {
	if v.ID == "" {
		v.ID, _ = newID()
	}
	if _, e := s.GetChannel(ctx, v.ChannelID); e != nil {
		return v, e
	}
	v.CreatedAt = s.clock().UTC()
	_, e := s.db.ExecContext(ctx, "INSERT INTO collaboration_threads(id,channel_id,title,root_message_id,created_by,created_at) VALUES(?,?,?,?,?,?)", v.ID, v.ChannelID, v.Title, v.RootMessageID, v.CreatedBy, v.CreatedAt.UnixMilli())
	return v, e
}

func (s *Service) CreateTask(ctx context.Context, v Task) (Task, error) {
	normalizedRequirements, capabilityErr := normalizeCapabilityList(v.RequiredCapabilities, 32)
	if capabilityErr != nil {
		return v, capabilityErr
	}
	v.RequiredCapabilities = normalizedRequirements
	if v.IdempotencyKey != "" {
		existing, err := s.findTaskByIdempotency(ctx, v.WorkspaceID, v.IdempotencyKey)
		if err == nil {
			if !sameCapabilities(existing.RequiredCapabilities, v.RequiredCapabilities) {
				return v, ErrConflict
			}
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return v, err
		}
	}
	if v.ID == "" {
		var err error
		v.ID, err = newID()
		if err != nil {
			return v, err
		}
	}
	if v.Type == "" {
		v.Type = "custom"
	}
	if v.Status == "" {
		v.Status = "open"
	}
	if v.Priority == 0 {
		v.Priority = 3
	}
	v.Title = strings.TrimSpace(v.Title)
	if v.WorkspaceID == "" || v.Title == "" || len(v.Title) > 240 || v.Priority < 1 || v.Priority > 5 {
		return v, ErrInvalid
	}
	w, err := s.GetWorkspace(ctx, v.WorkspaceID)
	if err != nil {
		return v, err
	}
	if v.ParentTaskID != "" {
		p, e := s.GetTask(ctx, v.ParentTaskID)
		if e != nil {
			return v, e
		}
		if p.WorkspaceID != v.WorkspaceID || p.DelegationDepth+1 > w.MaxDelegationDepth {
			return v, ErrInvalid
		}
		v.DelegationDepth = p.DelegationDepth + 1
	}
	capabilitiesJSON, err := json.Marshal(v.RequiredCapabilities)
	if err != nil {
		return v, err
	}
	now := s.clock().UTC()
	v.CreatedAt = now
	v.UpdatedAt = now
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "INSERT INTO collaboration_tasks(id,workspace_id,parent_task_id,title,description,task_type,status,priority,created_by,idempotency_key,delegation_depth,required_capabilities_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", v.ID, v.WorkspaceID, null(v.ParentTaskID), v.Title, v.Description, v.Type, v.Status, v.Priority, v.CreatedBy, null(v.IdempotencyKey), v.DelegationDepth, string(capabilitiesJSON), now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return v, err
	}
	if err = s.writeEventTx(ctx, tx, v.WorkspaceID, "task", v.ID, "task.created", v); err != nil {
		return v, err
	}
	return v, tx.Commit()
}
func (s *Service) findTaskByIdempotency(ctx context.Context, workspaceID, key string) (Task, error) {
	var v Task
	var parent, k sql.NullString
	var capabilitiesJSON string
	var c, u int64
	err := s.db.QueryRowContext(ctx, "SELECT id,workspace_id,parent_task_id,title,description,task_type,status,priority,created_by,idempotency_key,delegation_depth,required_capabilities_json,created_at,updated_at FROM collaboration_tasks WHERE workspace_id=? AND idempotency_key=?", workspaceID, key).Scan(&v.ID, &v.WorkspaceID, &parent, &v.Title, &v.Description, &v.Type, &v.Status, &v.Priority, &v.CreatedBy, &k, &v.DelegationDepth, &capabilitiesJSON, &c, &u)
	if err != nil {
		return v, err
	}
	if parent.Valid {
		v.ParentTaskID = parent.String
	}
	if k.Valid {
		v.IdempotencyKey = k.String
	}
	if err = json.Unmarshal([]byte(capabilitiesJSON), &v.RequiredCapabilities); err != nil {
		return v, err
	}
	v.CreatedAt = time.UnixMilli(c).UTC()
	v.UpdatedAt = time.UnixMilli(u).UTC()
	return v, nil
}
func (s *Service) GetTask(ctx context.Context, id string) (Task, error) {
	var v Task
	var p, k sql.NullString
	var capabilitiesJSON string
	var c, u int64
	e := s.db.QueryRowContext(ctx, "SELECT id,workspace_id,parent_task_id,title,description,task_type,status,priority,created_by,idempotency_key,delegation_depth,required_capabilities_json,created_at,updated_at FROM collaboration_tasks WHERE id=?", id).Scan(&v.ID, &v.WorkspaceID, &p, &v.Title, &v.Description, &v.Type, &v.Status, &v.Priority, &v.CreatedBy, &k, &v.DelegationDepth, &capabilitiesJSON, &c, &u)
	if errors.Is(e, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if e != nil {
		return v, e
	}
	if p.Valid {
		v.ParentTaskID = p.String
	}
	if k.Valid {
		v.IdempotencyKey = k.String
	}
	if e = json.Unmarshal([]byte(capabilitiesJSON), &v.RequiredCapabilities); e != nil {
		return v, e
	}
	v.CreatedAt = time.UnixMilli(c).UTC()
	v.UpdatedAt = time.UnixMilli(u).UTC()
	return v, nil
}
func (s *Service) ListTasks(ctx context.Context, wid string) ([]Task, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT id FROM collaboration_tasks WHERE workspace_id=? ORDER BY priority,created_at DESC LIMIT 500", wid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	out := make([]Task, 0, len(ids))
	for _, id := range ids {
		v, e := s.GetTask(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Service) AssignTask(ctx context.Context, tid, aid, by string) (Assignment, error) {
	t, e := s.GetTask(ctx, tid)
	if e != nil {
		return Assignment{}, e
	}
	a, e := s.GetAgent(ctx, aid)
	if e != nil {
		return Assignment{}, e
	}
	if t.WorkspaceID != a.WorkspaceID {
		return Assignment{}, ErrInvalid
	}
	if a.Status == "disabled" {
		return Assignment{}, ErrConflict
	}
	if !hasRequiredCapabilities(a.Capabilities, t.RequiredCapabilities) {
		return Assignment{}, ErrCapabilityMismatch
	}
	existing, e := s.findAssignment(ctx, tid, aid)
	if e == nil {
		return existing, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return Assignment{}, e
	}
	id, e := newID()
	if e != nil {
		return Assignment{}, e
	}
	now := s.clock().UTC()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Assignment{}, e
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, "INSERT INTO task_assignments(id,task_id,agent_id,status,assigned_by,created_at,updated_at) VALUES(?,?,?,'pending',?,?,?)", id, tid, aid, by, now.UnixMilli(), now.UnixMilli())
	if e != nil {
		_ = tx.Rollback()
		again, readErr := s.findAssignment(ctx, tid, aid)
		if readErr == nil {
			return again, nil
		}
		return Assignment{}, e
	}
	res, e := tx.ExecContext(ctx, "UPDATE collaboration_tasks SET status='assigned',updated_at=? WHERE id=? AND status IN ('open','assigned','blocked')", now.UnixMilli(), tid)
	if e != nil {
		return Assignment{}, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Assignment{}, ErrConflict
	}
	v := Assignment{ID: id, TaskID: tid, AgentID: aid, Status: "pending", AssignedBy: by, CreatedAt: now, UpdatedAt: now}
	if e = s.writeEventTx(ctx, tx, t.WorkspaceID, "task", tid, "task.assigned", v); e != nil {
		return Assignment{}, e
	}
	if e = tx.Commit(); e != nil {
		return Assignment{}, e
	}
	return v, nil
}
func (s *Service) findAssignment(ctx context.Context, taskID, agentID string) (Assignment, error) {
	var v Assignment
	var exp sql.NullInt64
	var c, u int64
	err := s.db.QueryRowContext(ctx, "SELECT id,task_id,agent_id,status,assigned_by,lease_owner,lease_expires_at,claim_version,created_at,updated_at FROM task_assignments WHERE task_id=? AND agent_id=?", taskID, agentID).Scan(&v.ID, &v.TaskID, &v.AgentID, &v.Status, &v.AssignedBy, &v.LeaseOwner, &exp, &v.ClaimVersion, &c, &u)
	if err != nil {
		return v, err
	}
	if exp.Valid {
		x := time.UnixMilli(exp.Int64).UTC()
		v.LeaseExpiresAt = &x
	}
	v.CreatedAt = time.UnixMilli(c).UTC()
	v.UpdatedAt = time.UnixMilli(u).UTC()
	return v, nil
}
func (s *Service) ClaimAssignment(ctx context.Context, id, owner string, lease time.Duration) (Assignment, error) {
	if strings.TrimSpace(owner) == "" || lease < time.Second || lease > 24*time.Hour {
		return Assignment{}, ErrInvalid
	}
	now := s.clock().UTC()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Assignment{}, e
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(ctx, `UPDATE task_assignments
 SET status='claimed',lease_owner=?,lease_expires_at=?,claim_version=claim_version+1,updated_at=?
 WHERE id=? AND (status='pending' OR (status='claimed' AND lease_expires_at<?))
 AND NOT EXISTS (
   SELECT 1 FROM task_assignments same_agent
   WHERE same_agent.agent_id=(SELECT agent_id FROM task_assignments WHERE id=?)
     AND same_agent.id<>? AND same_agent.status='claimed' AND same_agent.lease_expires_at>=?
 )
 AND (
   SELECT COUNT(*) FROM task_assignments active
   JOIN collaboration_tasks active_task ON active_task.id=active.task_id
   WHERE active_task.workspace_id=(SELECT workspace_id FROM collaboration_tasks WHERE id=(SELECT task_id FROM task_assignments WHERE id=?))
     AND active.status='claimed' AND active.lease_expires_at>=?
 ) < (
   SELECT max_agent_concurrency FROM collaboration_workspaces
   WHERE id=(SELECT workspace_id FROM collaboration_tasks WHERE id=(SELECT task_id FROM task_assignments WHERE id=?))
 )`, owner, now.Add(lease).UnixMilli(), now.UnixMilli(), id, now.UnixMilli(), id, id, now.UnixMilli(), id, now.UnixMilli(), id)
	if e != nil {
		return Assignment{}, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		var limit, active, agentActive int
		checkErr := tx.QueryRowContext(ctx, `SELECT w.max_agent_concurrency,
  (SELECT COUNT(*) FROM task_assignments a JOIN collaboration_tasks t ON t.id=a.task_id
   WHERE t.workspace_id=w.id AND a.status='claimed' AND a.lease_expires_at>=?),
  (SELECT COUNT(*) FROM task_assignments a
   WHERE a.agent_id=target_assignment.agent_id AND a.id<>target_assignment.id
     AND a.status='claimed' AND a.lease_expires_at>=?)
  FROM collaboration_workspaces w JOIN collaboration_tasks target ON target.workspace_id=w.id
  JOIN task_assignments target_assignment ON target_assignment.task_id=target.id
  WHERE target_assignment.id=?`, now.UnixMilli(), now.UnixMilli(), id).Scan(&limit, &active, &agentActive)
		if checkErr == nil && active >= limit {
			return Assignment{}, ErrWorkspaceConcurrencyLimit
		}
		if checkErr == nil && agentActive > 0 {
			return Assignment{}, ErrAgentBusy
		}
		return Assignment{}, ErrConflict
	}
	var v Assignment
	var exp, c, u int64
	e = tx.QueryRowContext(ctx, "SELECT id,task_id,agent_id,status,assigned_by,lease_owner,lease_expires_at,claim_version,created_at,updated_at FROM task_assignments WHERE id=?", id).Scan(&v.ID, &v.TaskID, &v.AgentID, &v.Status, &v.AssignedBy, &v.LeaseOwner, &exp, &v.ClaimVersion, &c, &u)
	if e != nil {
		return v, e
	}
	x := time.UnixMilli(exp).UTC()
	v.LeaseExpiresAt = &x
	v.CreatedAt = time.UnixMilli(c).UTC()
	v.UpdatedAt = time.UnixMilli(u).UTC()
	_, e = tx.ExecContext(ctx, "UPDATE collaboration_tasks SET status='in_progress',updated_at=? WHERE id=?", now.UnixMilli(), v.TaskID)
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}

// CompleteAssignment preserves the legacy completion contract for callers without a runtime result.
// CompleteAssignment preserves the legacy completion contract for callers without a runtime result.
func (s *Service) CompleteAssignment(ctx context.Context, id, owner string, version int64) error {
	return s.CompleteAssignmentWithResult(ctx, id, owner, version, "", nil)
}

// CompleteAssignmentWithResult atomically completes an assignment and publishes its
// execution result through the collaboration outbox. The outbox is the durable
// handoff boundary for downstream consumers.
func (s *Service) CompleteAssignmentWithResult(ctx context.Context, id, owner string, version int64, executionID string, result json.RawMessage) error {
	if len(result) > 4<<20 || (len(result) > 0 && !json.Valid(result)) || len(executionID) > 128 {
		return ErrInvalid
	}

	now := s.clock().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var taskID, workspaceID string
	err = tx.QueryRowContext(ctx, "SELECT a.task_id,t.workspace_id FROM task_assignments a JOIN collaboration_tasks t ON t.id=a.task_id WHERE a.id=?", id).Scan(&taskID, &workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	res, err := tx.ExecContext(ctx, "UPDATE task_assignments SET status='completed',lease_owner='',lease_expires_at=NULL,updated_at=? WHERE id=? AND status='claimed' AND lease_owner=? AND claim_version=? AND lease_expires_at>=?", now.UnixMilli(), id, owner, version, now.UnixMilli())
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrLeaseLost
	}

	_, err = tx.ExecContext(ctx, "UPDATE collaboration_tasks SET status='completed',updated_at=? WHERE id=?", now.UnixMilli(), taskID)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(map[string]any{
		"task_id":       taskID,
		"assignment_id": id,
		"execution_id":  executionID,
		"result":        result,
	})
	if err != nil {
		return err
	}
	eventID, err := newID()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO collaboration_outbox(id,workspace_id,aggregate_type,aggregate_id,event_type,payload_json,created_at) VALUES(?,?,?,?,?,?,?)", eventID, workspaceID, "task", taskID, "task.completed", string(payload), now.UnixMilli())
	if err != nil {
		return err
	}

	return tx.Commit()
}
func (s *Service) PendingOutbox(ctx context.Context, limit int) ([]Event, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	return s.listOutbox(ctx, `SELECT id,workspace_id,aggregate_type,aggregate_id,event_type,payload_json,attempts,last_error,created_at,published_at
  FROM collaboration_outbox WHERE published_at IS NULL ORDER BY created_at,id LIMIT ?`, limit)
}
func (s *Service) ListDeadLetters(ctx context.Context, limit int) ([]Event, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	return s.listOutbox(ctx, `SELECT id,workspace_id,aggregate_type,aggregate_id,event_type,payload_json,attempts,last_error,created_at,published_at
  FROM collaboration_outbox WHERE published_at IS NOT NULL AND last_error LIKE 'dead-letter:%'
  ORDER BY published_at DESC,id LIMIT ?`, limit)
}
func (s *Service) listOutbox(ctx context.Context, query string, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var v Event
		var payload string
		var created int64
		var published sql.NullInt64
		if err = rows.Scan(&v.ID, &v.WorkspaceID, &v.AggregateType, &v.AggregateID, &v.Type, &payload, &v.Attempts, &v.LastError, &created, &published); err != nil {
			return nil, err
		}
		v.Payload = json.RawMessage(payload)
		v.CreatedAt = time.UnixMilli(created).UTC()
		if published.Valid {
			at := time.UnixMilli(published.Int64).UTC()
			v.PublishedAt = &at
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) RequeueDeadLetter(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalid
	}
	res, err := s.db.ExecContext(ctx, `UPDATE collaboration_outbox SET published_at=NULL,next_attempt_at=0,attempts=0,last_error=''
  WHERE id=? AND published_at IS NOT NULL AND last_error LIKE 'dead-letter:%'`, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		return nil
	}
	var count int
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_outbox WHERE id=?", id).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return ErrConflict
}
func (s *Service) MarkPublished(ctx context.Context, id string) error {
	_, e := s.db.ExecContext(ctx, "UPDATE collaboration_outbox SET published_at=?,attempts=attempts+1,last_error='' WHERE id=? AND published_at IS NULL", s.clock().UTC().UnixMilli(), id)
	return e
}
func (s *Service) ReceiveEvent(ctx context.Context, agentID string, e Event) (bool, error) {
	if _, err := s.GetAgent(ctx, agentID); err != nil {
		return false, err
	}
	if e.ID == "" || e.Type == "" || !json.Valid(e.Payload) {
		return false, ErrInvalid
	}
	res, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO collaboration_inbox(id,agent_id,event_id,event_type,payload_json,status,received_at) VALUES(?,?,?,?,?,'pending',?)", e.ID+":"+agentID, agentID, e.ID, e.Type, string(e.Payload), s.clock().UTC().UnixMilli())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
func null(v string) any {
	if v == "" {
		return nil
	}
	return v
}

type DecisionRequest struct {
	AgentID      string `json:"agent_id"`
	EventID      string `json:"event_id,omitempty"`
	Action       string `json:"action"`
	ResourceType string `json:"resource_type,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
	Reason       string `json:"reason,omitempty"`
}
type DecisionResult struct {
	Outcome     string `json:"outcome"`
	Action      string `json:"action"`
	ApprovalID  string `json:"approval_id,omitempty"`
	Explanation string `json:"explanation"`
}
type Approval struct {
	ID               string     `json:"id"`
	WorkspaceID      string     `json:"workspace_id"`
	RequesterAgentID string     `json:"requester_agent_id,omitempty"`
	ReviewerID       string     `json:"reviewer_id,omitempty"`
	ResourceType     string     `json:"resource_type"`
	ResourceID       string     `json:"resource_id"`
	Action           string     `json:"action"`
	Reason           string     `json:"reason,omitempty"`
	Status           string     `json:"status"`
	DecisionNote     string     `json:"decision_note,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	DecidedAt        *time.Time `json:"decided_at,omitempty"`
}

type InboxEvent struct {
	ID         string          `json:"id"`
	AgentID    string          `json:"agent_id"`
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	Payload    json.RawMessage `json:"payload"`
	Status     string          `json:"status"`
	ReceivedAt time.Time       `json:"received_at"`
}

func (s *Service) ListInbox(ctx context.Context, agentID, status string, limit int) ([]InboxEvent, error) {
	if _, err := s.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if status == "" {
		status = "pending"
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,agent_id,event_id,event_type,payload_json,status,received_at FROM collaboration_inbox WHERE agent_id=? AND status=? ORDER BY received_at,id LIMIT ?", agentID, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InboxEvent{}
	for rows.Next() {
		var v InboxEvent
		var p string
		var t int64
		if err = rows.Scan(&v.ID, &v.AgentID, &v.EventID, &v.EventType, &p, &v.Status, &t); err != nil {
			return nil, err
		}
		v.Payload = json.RawMessage(p)
		v.ReceivedAt = time.UnixMilli(t).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) AckInbox(ctx context.Context, agentID, eventID, status string) error {
	if status != "handled" && status != "ignored" && status != "failed" {
		return ErrInvalid
	}
	res, err := s.db.ExecContext(ctx, "UPDATE collaboration_inbox SET status=?,handled_at=? WHERE agent_id=? AND event_id=? AND status='pending'", status, s.clock().UTC().UnixMilli(), agentID, eventID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Service) HandoffTask(ctx context.Context, parentID string, h Handoff, createdBy string) (Task, Assignment, error) {
	parent, err := s.GetTask(ctx, parentID)
	if err != nil {
		return Task{}, Assignment{}, err
	}
	h.Goal = strings.TrimSpace(h.Goal)
	h.Recipient = strings.TrimSpace(h.Recipient)
	h.RequesterAgentID = strings.TrimSpace(h.RequesterAgentID)
	h.IdempotencyKey = strings.TrimSpace(h.IdempotencyKey)
	if h.Goal == "" || h.Recipient == "" || h.RequesterAgentID == "" || h.IdempotencyKey == "" || len(h.Goal) > 240 {
		return Task{}, Assignment{}, ErrInvalid
	}
	requester, err := s.GetAgent(ctx, h.RequesterAgentID)
	if err != nil {
		return Task{}, Assignment{}, err
	}
	recipient, err := s.GetAgent(ctx, h.Recipient)
	if err != nil {
		return Task{}, Assignment{}, err
	}
	if requester.WorkspaceID != parent.WorkspaceID || recipient.WorkspaceID != parent.WorkspaceID {
		return Task{}, Assignment{}, ErrInvalid
	}
	if recipient.Status == "disabled" {
		return Task{}, Assignment{}, ErrConflict
	}
	h.RequiredCapabilities, err = normalizeCapabilityList(h.RequiredCapabilities, 32)
	if err != nil {
		return Task{}, Assignment{}, err
	}
	if !hasRequiredCapabilities(recipient.Capabilities, h.RequiredCapabilities) {
		return Task{}, Assignment{}, ErrCapabilityMismatch
	}
	workspace, err := s.GetWorkspace(ctx, parent.WorkspaceID)
	if err != nil {
		return Task{}, Assignment{}, err
	}
	if parent.DelegationDepth+1 > workspace.MaxDelegationDepth {
		return Task{}, Assignment{}, ErrInvalid
	}
	key := "handoff:" + parent.ID + ":" + recipient.ID + ":" + h.IdempotencyKey
	decision, err := s.EvaluateDecision(ctx, DecisionRequest{AgentID: requester.ID, Action: "delegate", ResourceType: "handoff", ResourceID: key, Reason: h.Reason})
	if err != nil {
		return Task{}, Assignment{}, err
	}
	if decision.Outcome != "allowed" {
		return Task{}, Assignment{}, &ApprovalRequiredError{ApprovalID: decision.ApprovalID}
	}
	payload, err := json.Marshal(h)
	if err != nil {
		return Task{}, Assignment{}, err
	}
	now := s.clock().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, Assignment{}, err
	}
	defer tx.Rollback()

	var child Task
	var existingParent, existingKey sql.NullString
	var requiredCapabilitiesJSON string
	var createdAt, updatedAt int64
	err = tx.QueryRowContext(ctx, `SELECT id,workspace_id,parent_task_id,title,description,task_type,status,priority,created_by,idempotency_key,delegation_depth,required_capabilities_json,created_at,updated_at
  FROM collaboration_tasks WHERE workspace_id=? AND idempotency_key=?`, parent.WorkspaceID, key).
		Scan(&child.ID, &child.WorkspaceID, &existingParent, &child.Title, &child.Description, &child.Type, &child.Status, &child.Priority, &child.CreatedBy, &existingKey, &child.DelegationDepth, &requiredCapabilitiesJSON, &createdAt, &updatedAt)
	if err == nil {
		if !existingParent.Valid || existingParent.String != parent.ID || child.Title != h.Goal || child.Description != string(payload) || !existingKey.Valid || existingKey.String != key {
			return Task{}, Assignment{}, ErrConflict
		}
		child.ParentTaskID = existingParent.String
		child.IdempotencyKey = existingKey.String
		if err = json.Unmarshal([]byte(requiredCapabilitiesJSON), &child.RequiredCapabilities); err != nil {
			return Task{}, Assignment{}, err
		}
		if !sameCapabilities(child.RequiredCapabilities, h.RequiredCapabilities) {
			return Task{}, Assignment{}, ErrConflict
		}
		child.CreatedAt = time.UnixMilli(createdAt).UTC()
		child.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	} else if errors.Is(err, sql.ErrNoRows) {
		childID, idErr := newID()
		if idErr != nil {
			return Task{}, Assignment{}, idErr
		}
		child = Task{ID: childID, WorkspaceID: parent.WorkspaceID, ParentTaskID: parent.ID, Title: h.Goal, Description: string(payload), Type: parent.Type, Status: "open", Priority: parent.Priority, CreatedBy: createdBy, IdempotencyKey: key, DelegationDepth: parent.DelegationDepth + 1, RequiredCapabilities: h.RequiredCapabilities, CreatedAt: now, UpdatedAt: now}
		capabilitiesJSON, marshalErr := json.Marshal(child.RequiredCapabilities)
		if marshalErr != nil {
			return Task{}, Assignment{}, marshalErr
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO collaboration_tasks(id,workspace_id,parent_task_id,title,description,task_type,status,priority,created_by,idempotency_key,delegation_depth,required_capabilities_json,created_at,updated_at)
   VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, child.ID, child.WorkspaceID, child.ParentTaskID, child.Title, child.Description, child.Type, child.Status, child.Priority, child.CreatedBy, child.IdempotencyKey, child.DelegationDepth, string(capabilitiesJSON), now.UnixMilli(), now.UnixMilli())
		if err != nil {
			return Task{}, Assignment{}, err
		}
		if err = s.writeEventTx(ctx, tx, child.WorkspaceID, "task", child.ID, "task.created", child); err != nil {
			return Task{}, Assignment{}, err
		}
	} else {
		return Task{}, Assignment{}, err
	}

	var assignment Assignment
	var leaseExpiry sql.NullInt64
	var assignmentCreated, assignmentUpdated int64
	err = tx.QueryRowContext(ctx, `SELECT id,task_id,agent_id,status,assigned_by,lease_owner,lease_expires_at,claim_version,created_at,updated_at
  FROM task_assignments WHERE task_id=? AND agent_id=?`, child.ID, recipient.ID).
		Scan(&assignment.ID, &assignment.TaskID, &assignment.AgentID, &assignment.Status, &assignment.AssignedBy, &assignment.LeaseOwner, &leaseExpiry, &assignment.ClaimVersion, &assignmentCreated, &assignmentUpdated)
	if err == nil {
		if leaseExpiry.Valid {
			v := time.UnixMilli(leaseExpiry.Int64).UTC()
			assignment.LeaseExpiresAt = &v
		}
		assignment.CreatedAt = time.UnixMilli(assignmentCreated).UTC()
		assignment.UpdatedAt = time.UnixMilli(assignmentUpdated).UTC()
		if err = tx.Commit(); err != nil {
			return Task{}, Assignment{}, err
		}
		return child, assignment, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Task{}, Assignment{}, err
	}
	if child.Status != "open" && child.Status != "assigned" {
		return Task{}, Assignment{}, ErrConflict
	}
	assignmentID, idErr := newID()
	if idErr != nil {
		return Task{}, Assignment{}, idErr
	}
	assignment = Assignment{ID: assignmentID, TaskID: child.ID, AgentID: recipient.ID, Status: "pending", AssignedBy: createdBy, CreatedAt: now, UpdatedAt: now}
	if _, err = tx.ExecContext(ctx, `INSERT INTO task_assignments(id,task_id,agent_id,status,assigned_by,created_at,updated_at)
  VALUES(?,?,?,'pending',?,?,?)`, assignment.ID, assignment.TaskID, assignment.AgentID, assignment.AssignedBy, now.UnixMilli(), now.UnixMilli()); err != nil {
		return Task{}, Assignment{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE collaboration_tasks SET status='assigned',updated_at=? WHERE id=? AND status IN ('open','assigned')`, now.UnixMilli(), child.ID)
	if err != nil {
		return Task{}, Assignment{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Task{}, Assignment{}, err
	}
	if affected != 1 {
		return Task{}, Assignment{}, ErrConflict
	}
	child.Status = "assigned"
	child.UpdatedAt = now
	if err = s.writeEventTx(ctx, tx, child.WorkspaceID, "task", child.ID, "task.assigned", assignment); err != nil {
		return Task{}, Assignment{}, err
	}
	if err = tx.Commit(); err != nil {
		return Task{}, Assignment{}, err
	}
	return child, assignment, nil
}
func (s *Service) RenewAssignment(ctx context.Context, id, owner string, version int64, lease time.Duration) (Assignment, error) {
	if strings.TrimSpace(owner) == "" || lease < time.Second || lease > 24*time.Hour {
		return Assignment{}, ErrInvalid
	}
	now := s.clock().UTC()
	expires := now.Add(lease)
	res, err := s.db.ExecContext(ctx, "UPDATE task_assignments SET lease_expires_at=?,updated_at=? WHERE id=? AND status='claimed' AND lease_owner=? AND claim_version=? AND lease_expires_at>=?", expires.UnixMilli(), now.UnixMilli(), id, owner, version, now.UnixMilli())
	if err != nil {
		return Assignment{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Assignment{}, ErrLeaseLost
	}
	var v Assignment
	var created, updated int64
	var exp int64
	err = s.db.QueryRowContext(ctx, "SELECT id,task_id,agent_id,status,assigned_by,lease_owner,lease_expires_at,claim_version,created_at,updated_at FROM task_assignments WHERE id=?", id).Scan(&v.ID, &v.TaskID, &v.AgentID, &v.Status, &v.AssignedBy, &v.LeaseOwner, &exp, &v.ClaimVersion, &created, &updated)
	if err != nil {
		return v, err
	}
	x := time.UnixMilli(exp).UTC()
	v.LeaseExpiresAt = &x
	v.CreatedAt = time.UnixMilli(created).UTC()
	v.UpdatedAt = time.UnixMilli(updated).UTC()
	return v, nil
}

func (s *Service) EvaluateDecision(ctx context.Context, req DecisionRequest) (DecisionResult, error) {
	agent, err := s.GetAgent(ctx, req.AgentID)
	if err != nil {
		return DecisionResult{}, err
	}
	allowedAction := map[string]bool{"ignore": true, "reply": true, "ask_human": true, "claim": true, "delegate": true}
	if !allowedAction[req.Action] {
		return DecisionResult{}, ErrInvalid
	}
	workspace, err := s.GetWorkspace(ctx, agent.WorkspaceID)
	if err != nil {
		return DecisionResult{}, err
	}
	requiresApproval := false
	switch workspace.AutonomyMode {
	case "directed":
		requiresApproval = req.Action == "claim" || req.Action == "delegate"
	case "hybrid":
		requiresApproval = req.Action == "delegate"
	case "autonomous":
		requiresApproval = false
	default:
		return DecisionResult{}, ErrInvalid
	}
	if requiresApproval {
		var existingID, existingStatus string
		lookupErr := s.db.QueryRowContext(ctx, "SELECT id,status FROM collaboration_approvals WHERE workspace_id=? AND requester_agent_id=? AND resource_type=? AND resource_id=? AND action=? AND status IN ('pending','approved') AND expires_at>? ORDER BY created_at DESC LIMIT 1", agent.WorkspaceID, agent.ID, req.ResourceType, req.ResourceID, req.Action, s.clock().UTC().UnixMilli()).Scan(&existingID, &existingStatus)
		if lookupErr == nil {
			if existingStatus == "approved" {
				requiresApproval = false
			} else {
				return DecisionResult{Outcome: "approval_required", Action: req.Action, ApprovalID: existingID, Explanation: "an approval request is already pending"}, nil
			}
		} else if !errors.Is(lookupErr, sql.ErrNoRows) {
			return DecisionResult{}, lookupErr
		}
	}
	outcome := "allowed"
	explanation := "decision is permitted by workspace autonomy policy"
	var approvalID string
	if requiresApproval {
		outcome = "approval_required"
		explanation = "workspace autonomy policy requires human approval"
		approvalID, err = newID()
		if err != nil {
			return DecisionResult{}, err
		}
	}
	auditID, err := newID()
	if err != nil {
		return DecisionResult{}, err
	}
	now := s.clock().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DecisionResult{}, err
	}
	defer tx.Rollback()
	if requiresApproval {
		expires := now.Add(24 * time.Hour)
		res, insertErr := tx.ExecContext(ctx, "INSERT OR IGNORE INTO collaboration_approvals(id,workspace_id,requester_agent_id,resource_type,resource_id,action,reason,status,created_at,expires_at) VALUES(?,?,?,?,?,?,?,'pending',?,?)", approvalID, agent.WorkspaceID, agent.ID, req.ResourceType, req.ResourceID, req.Action, req.Reason, now.UnixMilli(), expires.UnixMilli())
		if insertErr != nil {
			return DecisionResult{}, insertErr
		}
		inserted, rowsErr := res.RowsAffected()
		if rowsErr != nil {
			return DecisionResult{}, rowsErr
		}
		if inserted == 0 {
			lookupErr := tx.QueryRowContext(ctx, "SELECT id FROM collaboration_approvals WHERE workspace_id=? AND requester_agent_id=? AND resource_type=? AND resource_id=? AND action=? AND status='pending' AND expires_at>?", agent.WorkspaceID, agent.ID, req.ResourceType, req.ResourceID, req.Action, now.UnixMilli()).Scan(&approvalID)
			if lookupErr != nil {
				return DecisionResult{}, errors.Join(ErrConflict, lookupErr)
			}
		} else {
			if err = s.writeEventTx(ctx, tx, agent.WorkspaceID, "approval", approvalID, "approval.requested", map[string]any{"approval_id": approvalID, "agent_id": agent.ID, "action": req.Action, "resource_type": req.ResourceType, "resource_id": req.ResourceID}); err != nil {
				return DecisionResult{}, err
			}
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO collaboration_decision_audit(id,workspace_id,agent_id,event_id,action,resource_type,resource_id,outcome,approval_id,reason,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", auditID, agent.WorkspaceID, agent.ID, req.EventID, req.Action, req.ResourceType, req.ResourceID, outcome, null(approvalID), req.Reason, now.UnixMilli())
	if err != nil {
		return DecisionResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return DecisionResult{}, err
	}
	return DecisionResult{Outcome: outcome, Action: req.Action, ApprovalID: approvalID, Explanation: explanation}, nil
}
func (s *Service) ListApprovals(ctx context.Context, workspaceID, status string, limit int) ([]Approval, error) {
	if _, err := s.GetWorkspace(ctx, workspaceID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	query := "SELECT id,workspace_id,COALESCE(requester_agent_id,''),reviewer_id,resource_type,resource_id,action,reason,status,decision_note,created_at,expires_at,decided_at FROM collaboration_approvals WHERE workspace_id=?"
	args := []any{workspaceID}
	if status != "" {
		query += " AND status=?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		var v Approval
		var c, x int64
		var decided sql.NullInt64
		if err = rows.Scan(&v.ID, &v.WorkspaceID, &v.RequesterAgentID, &v.ReviewerID, &v.ResourceType, &v.ResourceID, &v.Action, &v.Reason, &v.Status, &v.DecisionNote, &c, &x, &decided); err != nil {
			return nil, err
		}
		v.CreatedAt = time.UnixMilli(c).UTC()
		v.ExpiresAt = time.UnixMilli(x).UTC()
		if decided.Valid {
			z := time.UnixMilli(decided.Int64).UTC()
			v.DecidedAt = &z
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) ResolveApproval(ctx context.Context, id, reviewer, status, note string) (Approval, error) {
	if status != "approved" && status != "rejected" {
		return Approval{}, ErrInvalid
	}
	if strings.TrimSpace(reviewer) == "" {
		return Approval{}, ErrInvalid
	}
	now := s.clock().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Approval{}, err
	}
	defer tx.Rollback()
	var workspaceID string
	var expires int64
	if err = tx.QueryRowContext(ctx, "SELECT workspace_id,expires_at FROM collaboration_approvals WHERE id=? AND status='pending'", id).Scan(&workspaceID, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Approval{}, ErrNotFound
		}
		return Approval{}, err
	}
	if expires < now.UnixMilli() {
		_, _ = tx.ExecContext(ctx, "UPDATE collaboration_approvals SET status='expired',decided_at=? WHERE id=? AND status='pending'", now.UnixMilli(), id)
		_ = tx.Commit()
		return Approval{}, ErrConflict
	}
	res, err := tx.ExecContext(ctx, "UPDATE collaboration_approvals SET status=?,reviewer_id=?,decision_note=?,decided_at=? WHERE id=? AND status='pending'", status, reviewer, note, now.UnixMilli(), id)
	if err != nil {
		return Approval{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Approval{}, ErrConflict
	}
	if err = s.writeEventTx(ctx, tx, workspaceID, "approval", id, "approval."+status, map[string]any{"approval_id": id, "reviewer_id": reviewer, "status": status, "note": note}); err != nil {
		return Approval{}, err
	}
	if err = tx.Commit(); err != nil {
		return Approval{}, err
	}
	var v Approval
	var c, x int64
	var decided sql.NullInt64
	err = s.db.QueryRowContext(ctx, "SELECT id,workspace_id,COALESCE(requester_agent_id,''),reviewer_id,resource_type,resource_id,action,reason,status,decision_note,created_at,expires_at,decided_at FROM collaboration_approvals WHERE id=?", id).Scan(&v.ID, &v.WorkspaceID, &v.RequesterAgentID, &v.ReviewerID, &v.ResourceType, &v.ResourceID, &v.Action, &v.Reason, &v.Status, &v.DecisionNote, &c, &x, &decided)
	if err != nil {
		return v, err
	}
	v.CreatedAt = time.UnixMilli(c).UTC()
	v.ExpiresAt = time.UnixMilli(x).UTC()
	if decided.Valid {
		z := time.UnixMilli(decided.Int64).UTC()
		v.DecidedAt = &z
	}
	return v, nil
}
