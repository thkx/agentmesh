CREATE TABLE collaboration_approvals (
 id TEXT PRIMARY KEY NOT NULL,
 workspace_id TEXT NOT NULL REFERENCES collaboration_workspaces(id) ON DELETE CASCADE,
 requester_agent_id TEXT REFERENCES agent_identities(id) ON DELETE SET NULL,
 reviewer_id TEXT NOT NULL DEFAULT '',
 resource_type TEXT NOT NULL,
 resource_id TEXT NOT NULL,
 action TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected','expired')),
 decision_note TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 decided_at INTEGER
);
CREATE INDEX idx_collaboration_approvals_workspace_status ON collaboration_approvals(workspace_id,status,created_at DESC);
CREATE INDEX idx_collaboration_approvals_resource ON collaboration_approvals(resource_type,resource_id);
CREATE TABLE collaboration_decision_audit (
 id TEXT PRIMARY KEY NOT NULL,
 workspace_id TEXT NOT NULL REFERENCES collaboration_workspaces(id) ON DELETE CASCADE,
 agent_id TEXT NOT NULL REFERENCES agent_identities(id) ON DELETE CASCADE,
 event_id TEXT NOT NULL DEFAULT '',
 action TEXT NOT NULL,
 resource_type TEXT NOT NULL DEFAULT '',
 resource_id TEXT NOT NULL DEFAULT '',
 outcome TEXT NOT NULL CHECK(outcome IN ('allowed','approval_required','denied')),
 approval_id TEXT,
 reason TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL
);
CREATE INDEX idx_collaboration_decision_audit_agent ON collaboration_decision_audit(agent_id,created_at DESC);
