DROP INDEX IF EXISTS idx_tool_approvals_expiry;
DROP TABLE IF EXISTS tool_approvals;
CREATE TABLE tool_execution_audit_rollback (
 id TEXT PRIMARY KEY NOT NULL,
 tool_name TEXT NOT NULL,
 workspace TEXT NOT NULL DEFAULT '',
 allowed INTEGER NOT NULL CHECK(allowed IN (0,1)),
 approved INTEGER NOT NULL CHECK(approved IN (0,1)),
 started_at INTEGER NOT NULL,
 duration_ns INTEGER NOT NULL DEFAULT 0,
 error_text TEXT NOT NULL DEFAULT ''
);
INSERT INTO tool_execution_audit_rollback(id,tool_name,workspace,allowed,approved,started_at,duration_ns,error_text)
 SELECT id,tool_name,workspace,allowed,approved,started_at,duration_ns,error_text FROM tool_execution_audit;
DROP TABLE tool_execution_audit;
ALTER TABLE tool_execution_audit_rollback RENAME TO tool_execution_audit;
CREATE INDEX idx_tool_execution_audit_started ON tool_execution_audit(started_at DESC);
