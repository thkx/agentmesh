ALTER TABLE tool_execution_audit ADD COLUMN authorization TEXT NOT NULL DEFAULT '';
CREATE TABLE tool_approvals (
 token_hash TEXT PRIMARY KEY NOT NULL,
 tool_name TEXT NOT NULL,
 workspace TEXT NOT NULL,
 args_hash TEXT NOT NULL,
 expires_at INTEGER NOT NULL,
 created_at INTEGER NOT NULL,
 used_at INTEGER
);
CREATE INDEX idx_tool_approvals_expiry ON tool_approvals(expires_at);
