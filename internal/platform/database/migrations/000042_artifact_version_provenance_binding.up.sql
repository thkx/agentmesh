-- Preserve P3 Artifact Version immutability while allowing M44 to bind
-- execution provenance exactly once. Content and version identity remain frozen.
DROP TRIGGER IF EXISTS collaboration_artifact_versions_no_update;

CREATE TRIGGER collaboration_artifact_versions_content_immutable
BEFORE UPDATE ON collaboration_artifact_versions
WHEN OLD.id IS NOT NEW.id
  OR OLD.artifact_id IS NOT NEW.artifact_id
  OR OLD.version IS NOT NEW.version
  OR OLD.content IS NOT NEW.content
  OR OLD.content_sha256 IS NOT NEW.content_sha256
  OR OLD.media_type IS NOT NEW.media_type
  OR OLD.size_bytes IS NOT NEW.size_bytes
  OR OLD.metadata_json IS NOT NEW.metadata_json
  OR OLD.creator_agent_id IS NOT NEW.creator_agent_id
  OR OLD.created_at IS NOT NEW.created_at
BEGIN
    SELECT RAISE(ABORT, 'artifact version content and identity are immutable');
END;

-- Migration 000041's provenance guard remains responsible for validating
-- the only mutable-once fields: source_execution_id and source_attempt_id.
