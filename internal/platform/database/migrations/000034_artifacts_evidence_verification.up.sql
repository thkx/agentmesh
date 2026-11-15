CREATE TABLE collaboration_artifacts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES collaboration_workspaces(id) ON DELETE CASCADE,
    source_task_id TEXT REFERENCES collaboration_tasks(id) ON DELETE SET NULL,
    creator_agent_id TEXT REFERENCES agent_identities(id) ON DELETE SET NULL,
    kind TEXT NOT NULL,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','submitted','accepted','rejected')),
    current_version INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_artifacts_workspace_updated ON collaboration_artifacts(workspace_id,updated_at DESC);
CREATE INDEX idx_artifacts_task ON collaboration_artifacts(source_task_id);

CREATE TABLE collaboration_artifact_versions (
    id TEXT PRIMARY KEY,
    artifact_id TEXT NOT NULL REFERENCES collaboration_artifacts(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    content BLOB NOT NULL,
    content_sha256 TEXT NOT NULL,
    media_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    creator_agent_id TEXT REFERENCES agent_identities(id) ON DELETE SET NULL,
    source_execution_id TEXT,
    created_at INTEGER NOT NULL,
    UNIQUE(artifact_id,version),
    UNIQUE(artifact_id,content_sha256)
);
CREATE INDEX idx_artifact_versions_artifact ON collaboration_artifact_versions(artifact_id,version DESC);

CREATE TABLE collaboration_artifact_evidence (
    id TEXT PRIMARY KEY,
    artifact_id TEXT NOT NULL REFERENCES collaboration_artifacts(id) ON DELETE CASCADE,
    version_id TEXT NOT NULL REFERENCES collaboration_artifact_versions(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES collaboration_workspaces(id) ON DELETE CASCADE,
    task_id TEXT REFERENCES collaboration_tasks(id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK(kind IN ('test','build','diff','source_reference','tool_log','metric','manual')),
    source_ref TEXT NOT NULL DEFAULT '',
    content_sha256 TEXT NOT NULL DEFAULT '',
    statement TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_artifact_evidence_version ON collaboration_artifact_evidence(version_id,kind);

CREATE TABLE collaboration_artifact_verifications (
    id TEXT PRIMARY KEY,
    artifact_id TEXT NOT NULL REFERENCES collaboration_artifacts(id) ON DELETE CASCADE,
    version_id TEXT NOT NULL REFERENCES collaboration_artifact_versions(id) ON DELETE CASCADE,
    outcome TEXT NOT NULL CHECK(outcome IN ('passed','needs_evidence','failed')),
    policy_json TEXT NOT NULL,
    evidence_summary_json TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_artifact_verifications_artifact ON collaboration_artifact_verifications(artifact_id,created_at DESC);

CREATE TRIGGER collaboration_artifact_versions_no_update
BEFORE UPDATE ON collaboration_artifact_versions
BEGIN SELECT RAISE(ABORT, 'artifact versions are immutable'); END;
CREATE TRIGGER collaboration_artifact_versions_no_delete
BEFORE DELETE ON collaboration_artifact_versions
BEGIN SELECT RAISE(ABORT, 'artifact versions are immutable'); END;
CREATE TRIGGER collaboration_artifact_evidence_no_update
BEFORE UPDATE ON collaboration_artifact_evidence
BEGIN SELECT RAISE(ABORT, 'artifact evidence is append-only'); END;
CREATE TRIGGER collaboration_artifact_evidence_no_delete
BEFORE DELETE ON collaboration_artifact_evidence
BEGIN SELECT RAISE(ABORT, 'artifact evidence is append-only'); END;
