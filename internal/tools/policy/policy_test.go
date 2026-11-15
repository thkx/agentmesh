package policy

import (
	"context"
	"encoding/json"
	dom "github.com/thkx/agentmesh/internal/domain/tool"
	"testing"
)

func TestStaticPolicy(t *testing.T) {
	p := Static{ReadEnabled: true, WriteEnabled: true, ExecuteEnabled: false, RequireApprovalForWrite: true}
	read := dom.Spec{Permission: dom.PermissionRead}
	if d := p.Authorize(context.Background(), read, nil, "/ws"); !d.Allowed {
		t.Fatal("read should be allowed")
	}
	write := dom.Spec{Permission: dom.PermissionWrite}
	if d := p.Authorize(context.Background(), write, json.RawMessage(`{}`), "/ws"); !d.RequiresApproval || d.Allowed {
		t.Fatal("write should require approval")
	}
	exec := dom.Spec{Permission: dom.PermissionExecute}
	if d := p.Authorize(context.Background(), exec, nil, "/ws"); d.Allowed {
		t.Fatal("execute should be denied")
	}
	if d := p.Authorize(context.Background(), read, nil, ""); d.Allowed {
		t.Fatal("workspace should be required")
	}
}
