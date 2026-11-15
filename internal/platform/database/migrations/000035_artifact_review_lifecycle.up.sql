ALTER TABLE collaboration_artifacts ADD COLUMN submitted_version_id TEXT REFERENCES collaboration_artifact_versions(id);

CREATE TABLE collaboration_artifact_reviews (
    id TEXT PRIMARY KEY,
    artifact_id TEXT NOT NULL REFERENCES collaboration_artifacts(id) ON DELETE CASCADE,
    version_id TEXT NOT NULL REFERENCES collaboration_artifact_versions(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES collaboration_workspaces(id) ON DELETE CASCADE,
    reviewer_id TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK(outcome IN ('accepted','rejected')),
    note TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    UNIQUE(artifact_id,version_id)
);
CREATE INDEX idx_artifact_reviews_workspace_created ON collaboration_artifact_reviews(workspace_id,created_at DESC);

CREATE TRIGGER collaboration_artifact_reviews_no_update
BEFORE UPDATE ON collaboration_artifact_reviews
BEGIN SELECT RAISE(ABORT, 'artifact reviews are append-only'); END;
CREATE TRIGGER collaboration_artifact_reviews_no_delete
BEFORE DELETE ON collaboration_artifact_reviews
BEGIN SELECT RAISE(ABORT, 'artifact reviews are append-only'); END;
CREATE TRIGGER collaboration_artifact_verifications_no_update
BEFORE UPDATE ON collaboration_artifact_verifications
BEGIN SELECT RAISE(ABORT, 'artifact verifications are append-only'); END;
CREATE TRIGGER collaboration_artifact_verifications_no_delete
BEFORE DELETE ON collaboration_artifact_verifications
BEGIN SELECT RAISE(ABORT, 'artifact verifications are append-only'); END;
