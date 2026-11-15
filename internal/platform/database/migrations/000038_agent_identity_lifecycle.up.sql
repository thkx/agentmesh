-- Keep Agent Identity deletion recoverable so historical assignments, results,
-- approvals and audit records remain referentially intact.
CREATE TABLE agent_identity_lifecycle (
    agent_id TEXT PRIMARY KEY NOT NULL REFERENCES agent_identities(id) ON DELETE CASCADE,
    deleted_at INTEGER,
    recovered_at INTEGER
);
CREATE INDEX idx_agent_identity_lifecycle_deleted
    ON agent_identity_lifecycle(deleted_at);
