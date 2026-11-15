DROP TRIGGER IF EXISTS collaboration_artifact_versions_content_immutable;

-- Restore the original P3 blanket immutability rule on rollback.
CREATE TRIGGER collaboration_artifact_versions_no_update
BEFORE UPDATE ON collaboration_artifact_versions
BEGIN
    SELECT RAISE(ABORT, 'artifact versions are immutable');
END;
