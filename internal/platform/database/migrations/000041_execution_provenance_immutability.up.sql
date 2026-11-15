-- Harden provenance writes after migration 000040 introduced the optional
-- Execution/Attempt links. A provenance link may be filled once, then is immutable.
CREATE TRIGGER artifact_version_provenance_validate_update
BEFORE UPDATE OF source_execution_id, source_attempt_id ON collaboration_artifact_versions
WHEN COALESCE(OLD.source_execution_id,'') <> COALESCE(NEW.source_execution_id,'')
  OR COALESCE(OLD.source_attempt_id,'') <> COALESCE(NEW.source_attempt_id,'')
BEGIN
    SELECT CASE
        WHEN OLD.source_attempt_id IS NOT NULL
             AND COALESCE(OLD.source_attempt_id,'') <> COALESCE(NEW.source_attempt_id,'')
        THEN RAISE(ABORT, 'artifact version attempt provenance is immutable')
        WHEN OLD.source_execution_id IS NOT NULL AND OLD.source_execution_id <> ''
             AND COALESCE(OLD.source_execution_id,'') <> COALESCE(NEW.source_execution_id,'')
        THEN RAISE(ABORT, 'artifact version execution provenance is immutable')
        WHEN NEW.source_attempt_id IS NOT NULL
             AND (NEW.source_execution_id IS NULL OR NEW.source_execution_id = '')
        THEN RAISE(ABORT, 'source_attempt_id requires source_execution_id')
        WHEN NEW.source_attempt_id IS NOT NULL
             AND NOT EXISTS (
                 SELECT 1 FROM execution_attempts a
                 WHERE a.id=NEW.source_attempt_id AND a.execution_id=NEW.source_execution_id
             )
        THEN RAISE(ABORT, 'artifact source attempt does not belong to source execution')
        WHEN NEW.source_execution_id IS NOT NULL AND NEW.source_execution_id <> ''
             AND NOT EXISTS (SELECT 1 FROM executions e WHERE e.id=NEW.source_execution_id)
        THEN RAISE(ABORT, 'artifact source execution does not exist')
    END;
END;

CREATE TRIGGER artifact_evidence_provenance_validate_update
BEFORE UPDATE OF execution_id, attempt_id ON collaboration_artifact_evidence
WHEN COALESCE(OLD.execution_id,'') <> COALESCE(NEW.execution_id,'')
  OR COALESCE(OLD.attempt_id,'') <> COALESCE(NEW.attempt_id,'')
BEGIN
    SELECT CASE
        WHEN OLD.attempt_id IS NOT NULL
             AND COALESCE(OLD.attempt_id,'') <> COALESCE(NEW.attempt_id,'')
        THEN RAISE(ABORT, 'artifact evidence attempt provenance is immutable')
        WHEN OLD.execution_id IS NOT NULL
             AND COALESCE(OLD.execution_id,'') <> COALESCE(NEW.execution_id,'')
        THEN RAISE(ABORT, 'artifact evidence execution provenance is immutable')
        WHEN NEW.attempt_id IS NOT NULL AND NEW.execution_id IS NULL
        THEN RAISE(ABORT, 'attempt evidence requires execution_id')
        WHEN NEW.attempt_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM execution_attempts a
            WHERE a.id=NEW.attempt_id AND a.execution_id=NEW.execution_id
        )
        THEN RAISE(ABORT, 'evidence attempt does not belong to execution')
        WHEN NEW.execution_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM executions e WHERE e.id=NEW.execution_id
        )
        THEN RAISE(ABORT, 'evidence execution does not exist')
        WHEN NEW.execution_id IS NOT NULL AND EXISTS (
            SELECT 1 FROM collaboration_artifact_versions v
            WHERE v.id=NEW.version_id
              AND NULLIF(v.source_execution_id,'') IS NOT NULL
              AND v.source_execution_id <> NEW.execution_id
        )
        THEN RAISE(ABORT, 'evidence execution differs from artifact source execution')
    END;
END;

CREATE TRIGGER artifact_version_provenance_validate_insert
BEFORE INSERT ON collaboration_artifact_versions
WHEN NEW.source_execution_id IS NOT NULL AND NEW.source_execution_id <> ''
BEGIN
    SELECT CASE
        WHEN NOT EXISTS (SELECT 1 FROM executions e WHERE e.id=NEW.source_execution_id)
        THEN RAISE(ABORT, 'artifact source execution does not exist')
    END;
END;
