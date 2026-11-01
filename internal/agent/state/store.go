package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

var ErrNotFound = errors.New("agent state not found")

type Snapshot struct {
	Version   int                    `json:"version"`
	Task      dom.Task               `json:"task"`
	Result    dom.Result             `json:"result"`
	Messages  []modelgateway.Message `json:"messages"`
	UpdatedAt time.Time              `json:"updated_at"`
}

type Store interface {
	Save(context.Context, Snapshot) error
	Load(context.Context, string) (Snapshot, error)
}

func (s Snapshot) Validate() error {
	if s.Version != 1 {
		return errors.New("unsupported agent state version")
	}
	if err := s.Task.Validate(); err != nil {
		return err
	}
	if s.Result.TaskID != s.Task.ID {
		return errors.New("snapshot task id mismatch")
	}
	if len(s.Messages) > 10000 {
		return errors.New("snapshot contains too many messages")
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(encoded) > 16<<20 {
		return errors.New("snapshot exceeds 16 MiB limit")
	}
	for _, m := range s.Messages {
		if len(m.Content) > 2<<20 {
			return errors.New("snapshot message too large")
		}
		for _, call := range m.ToolCalls {
			if err := call.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

func Encode(s Snapshot) ([]byte, error) { return json.Marshal(s) }
