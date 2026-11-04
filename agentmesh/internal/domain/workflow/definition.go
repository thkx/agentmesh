package workflow

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidDefinition  = errors.New("invalid workflow definition")
	ErrNotFound           = errors.New("workflow not found")
	ErrInvalidTransition  = errors.New("invalid workflow state transition")
	ErrRecoveryNotAllowed = errors.New("workflow node recovery is not allowed")
)

type NodeType string

const (
	NodeStart     NodeType = "start"
	NodeAgent     NodeType = "agent"
	NodeCondition NodeType = "condition"
	NodeApproval  NodeType = "approval"
	NodeEnd       NodeType = "end"
)

type Definition struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Version   int       `json:"version"`
	Entry     string    `json:"entry"`
	Nodes     []Node    `json:"nodes"`
	Edges     []Edge    `json:"edges"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}
type Node struct {
	ID        string           `json:"id"`
	Type      NodeType         `json:"type"`
	Name      string           `json:"name,omitempty"`
	Agent     *AgentConfig     `json:"agent,omitempty"`
	Condition *ConditionConfig `json:"condition,omitempty"`
	Approval  *ApprovalConfig  `json:"approval,omitempty"`
}
type AgentConfig struct {
	AgentProfile  string   `json:"agent_profile,omitempty"`
	ModelID       string   `json:"model_id"`
	WorkspaceID   string   `json:"workspace_id"`
	Prompt        string   `json:"prompt"`
	MaxSteps      int      `json:"max_steps,omitempty"`
	MaxToolCalls  int      `json:"max_tool_calls,omitempty"`
	ApprovedTools []string `json:"approved_tools,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
}
type ConditionConfig struct {
	InputKey    string `json:"input_key"`
	Equals      any    `json:"equals"`
	TrueHandle  string `json:"true_handle,omitempty"`
	FalseHandle string `json:"false_handle,omitempty"`
}
type ApprovalConfig struct {
	Message string `json:"message"`
}
type Edge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Handle string `json:"handle,omitempty"`
}
type RunStatus string

const (
	RunQueued          RunStatus = "queued"
	RunRunning         RunStatus = "running"
	RunWaitingApproval RunStatus = "waiting_approval"
	RunSucceeded       RunStatus = "succeeded"
	RunFailed          RunStatus = "failed"
	RunRejected        RunStatus = "rejected"
	RunCanceled        RunStatus = "canceled"
)

type NodeStatus string

const (
	NodePending         NodeStatus = "pending"
	NodeRunning         NodeStatus = "running"
	NodeSucceeded       NodeStatus = "succeeded"
	NodeWaitingApproval NodeStatus = "waiting_approval"
	NodeFailed          NodeStatus = "failed"
	NodeSkipped         NodeStatus = "skipped"
	NodeInterrupted     NodeStatus = "interrupted"
)

type NodeState struct {
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	Status         NodeStatus      `json:"status"`
	Output         json.RawMessage `json:"output,omitempty"`
	Error          string          `json:"error,omitempty"`
	StartedAt      time.Time       `json:"started_at,omitempty"`
	FinishedAt     time.Time       `json:"finished_at,omitempty"`
	Attempt        int             `json:"attempt,omitempty"`
	WorkerID       string          `json:"worker_id,omitempty"`
	DurationMillis int64           `json:"duration_ms,omitempty"`
	ManualSkip     bool            `json:"manual_skip,omitempty"`
}
type Run struct {
	ID              string               `json:"id"`
	WorkflowID      string               `json:"workflow_id"`
	WorkflowVersion int                  `json:"workflow_version"`
	Status          RunStatus            `json:"status"`
	CurrentNode     string               `json:"current_node,omitempty"`
	PendingNodes    []string             `json:"pending_nodes,omitempty"`
	Input           json.RawMessage      `json:"input,omitempty"`
	Context         map[string]any       `json:"context"`
	Nodes           map[string]NodeState `json:"nodes"`
	ApprovalMessage string               `json:"approval_message,omitempty"`
	Error           string               `json:"error,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	FinishedAt      time.Time            `json:"finished_at,omitempty"`
}

func (d Definition) ValidateShape() error {
	if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.Name) == "" || d.Version < 1 || strings.TrimSpace(d.Entry) == "" || len(d.Nodes) == 0 || len(d.Nodes) > 256 || len(d.Edges) > 1024 {
		return ErrInvalidDefinition
	}
	return nil
}

// Event is an immutable, per-run ordered workflow history record.
type Event struct {
	ID         int64           `json:"id"`
	RunID      string          `json:"run_id"`
	Sequence   int64           `json:"sequence"`
	Type       string          `json:"type"`
	NodeID     string          `json:"node_id,omitempty"`
	WorkerID   string          `json:"worker_id,omitempty"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data,omitempty"`
}

type NodeObservation struct {
	NodeID         string     `json:"node_id"`
	Status         NodeStatus `json:"status"`
	Attempt        int        `json:"attempt"`
	WorkerID       string     `json:"worker_id,omitempty"`
	StartedAt      time.Time  `json:"started_at,omitempty"`
	FinishedAt     time.Time  `json:"finished_at,omitempty"`
	DurationMillis int64      `json:"duration_ms,omitempty"`
	Error          string     `json:"error,omitempty"`
}
