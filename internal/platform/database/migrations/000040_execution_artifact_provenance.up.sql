-- Artifact versions already carry source_execution_id. Add the precise
-- Attempt pointer, then enrich evidence with the same provenance chain.
ALTER TABLE collaboration_artifact_versions
    ADD COLUMN source_attempt_id TEXT REFERENCES execution_attempts(id) ON DELETE RESTRICT;

ALTER TABLE collaboration_artifact_evidence
    ADD COLUMN execution_id TEXT REFERENCES executions(id) ON DELETE RESTRICT;
ALTER TABLE collaboration_artifact_evidence
    ADD COLUMN attempt_id TEXT REFERENCES execution_attempts(id) ON DELETE RESTRICT;

-- Backfill the execution pointer for existing evidence where its immutable
-- artifact version already recorded a source execution.
UPDATE collaboration_artifact_evidence
SET execution_id = (
    SELECT NULLIF(v.source_execution_id, '')
    FROM collaboration_artifact_versions v
    WHERE v.id = collaboration_artifact_evidence.version_id
)
WHERE EXISTS (
    SELECT 1
    FROM collaboration_artifact_versions v
    WHERE v.id = collaboration_artifact_evidence.version_id
      AND NULLIF(v.source_execution_id, '') IS NOT NULL
);

CREATE INDEX idx_artifact_versions_source_execution
    ON collaboration_artifact_versions(source_execution_id)
    WHERE source_execution_id IS NOT NULL AND source_execution_id <> '';
CREATE INDEX idx_artifact_versions_source_attempt
    ON collaboration_artifact_versions(source_attempt_id)
    WHERE source_attempt_id IS NOT NULL;
CREATE INDEX idx_artifact_evidence_execution
    ON collaboration_artifact_evidence(execution_id, created_at)
    WHERE execution_id IS NOT NULL;
CREATE INDEX idx_artifact_evidence_attempt
    ON collaboration_artifact_evidence(attempt_id, created_at)
    WHERE attempt_id IS NOT NULL;

-- An evidence row may omit execution provenance for legacy/manual evidence.
-- If an Attempt is supplied, its Execution must agree with the explicit
-- execution_id and with the source Execution on the immutable Artifact Version.
CREATE TRIGGER artifact_evidence_validate_provenance_insert
BEFORE INSERT ON collaboration_artifact_evidence
WHEN NEW.attempt_id IS NOT NULL
BEGIN
    SELECT CASE
        WHEN NEW.execution_id IS NULL
        THEN RAISE(ABORT, 'attempt evidence requires execution_id')
        WHEN NOT EXISTS (
            SELECT 1 FROM execution_attempts a
            WHERE a.id = NEW.attempt_id AND a.execution_id = NEW.execution_id
        )
        THEN RAISE(ABORT, 'evidence attempt does not belong to execution')
        WHEN EXISTS (
            SELECT 1 FROM collaboration_artifact_versions v
            WHERE v.id = NEW.version_id
              AND NULLIF(v.source_execution_id, '') IS NOT NULL
              AND v.source_execution_id <> NEW.execution_id
        )
        THEN RAISE(ABORT, 'evidence execution differs from artifact source execution')
    END;
END;

CREATE TRIGGER artifact_version_validate_attempt_insert
BEFORE INSERT ON collaboration_artifact_versions
WHEN NEW.source_attempt_id IS NOT NULL
BEGIN
    SELECT CASE
        WHEN NEW.source_execution_id IS NULL OR NEW.source_execution_id = ''
        THEN RAISE(ABORT, 'source_attempt_id requires source_execution_id')
        WHEN NOT EXISTS (
            SELECT 1 FROM execution_attempts a
            WHERE a.id = NEW.source_attempt_id AND a.execution_id = NEW.source_execution_id
        )
        THEN RAISE(ABORT, 'artifact source attempt does not belong to source execution')
    END;
END;
