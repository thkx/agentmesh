package policy

import (
	"context"
	"encoding/json"
	dom "github.com/thkx/agentmesh/internal/domain/tool"
	"github.com/thkx/agentmesh/internal/tools"
)

type Static struct {
	ReadEnabled               bool
	WriteEnabled              bool
	ExecuteEnabled            bool
	RequireApprovalForWrite   bool
	RequireApprovalForExecute bool
}

func (p Static) Authorize(_ context.Context, s dom.Spec, _ json.RawMessage, workspace string) tools.Decision {
	if workspace == "" {
		return tools.Decision{Reason: "workspace is required"}
	}
	switch s.Permission {
	case dom.PermissionRead:
		if p.ReadEnabled {
			return tools.Decision{Allowed: true}
		}
		return tools.Decision{Reason: "read tools disabled"}
	case dom.PermissionWrite:
		if !p.WriteEnabled {
			return tools.Decision{Reason: "write tools disabled"}
		}
		if p.RequireApprovalForWrite {
			return tools.Decision{RequiresApproval: true, Reason: "write approval required"}
		}
		return tools.Decision{Allowed: true}
	case dom.PermissionExecute:
		if !p.ExecuteEnabled {
			return tools.Decision{Reason: "execute tools disabled"}
		}
		if p.RequireApprovalForExecute {
			return tools.Decision{RequiresApproval: true, Reason: "execution approval required"}
		}
		return tools.Decision{Allowed: true}
	default:
		return tools.Decision{Reason: "unknown permission"}
	}
}
