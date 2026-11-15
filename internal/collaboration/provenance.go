package collaboration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ArtifactProvenanceVersion describes the immutable origin of one artifact version.
type ArtifactProvenanceVersion struct {
	ID                string    `json:"id"`
	Version           int       `json:"version"`
	SourceExecutionID string    `json:"source_execution_id,omitempty"`
	SourceAttemptID   string    `json:"source_attempt_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// ArtifactProvenanceEvidence is evidence attached to an exact artifact version.
// Execution and Attempt are optional for legacy/manual evidence.
type ArtifactProvenanceEvidence struct {
	ID          string    `json:"id"`
	VersionID   string    `json:"version_id"`
	Kind        string    `json:"kind"`
	SourceRef   string    `json:"source_ref,omitempty"`
	ExecutionID string    `json:"execution_id,omitempty"`
	AttemptID   string    `json:"attempt_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type ArtifactProvenance struct {
	ArtifactID string                        `json:"artifact_id"`
	Versions   []ArtifactProvenanceVersion   `json:"versions"`
	Evidence   []ArtifactProvenanceEvidence  `json:"evidence"`
}

type BindArtifactVersionAttemptRequest struct {
	ExecutionID string
	AttemptID   string
}

type CreateArtifactEvidenceProvenanceRequest struct {
	VersionID     string
	Kind          string
	SourceRef     string
	ContentSHA256 string
	Statement     string
	CreatedBy     string
	ExecutionID   string
	AttemptID     string
}

// BindArtifactVersionAttempt fills the provenance pointer for an existing
// immutable version. The write is idempotent for the same pair and conflicts
// if another Attempt has already been attached.
func (s *Service) BindArtifactVersionAttempt(ctx context.Context, artifactID string, versionNumber int, in BindArtifactVersionAttemptRequest) error {
	artifactID = strings.TrimSpace(artifactID)
	in.ExecutionID = strings.TrimSpace(in.ExecutionID)
	in.AttemptID = strings.TrimSpace(in.AttemptID)
	if artifactID == "" || versionNumber < 1 || in.ExecutionID == "" || in.AttemptID == "" {
		return ErrInvalid
	}

	var sourceExecution, sourceAttempt string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(source_execution_id,''),COALESCE(source_attempt_id,'')
		FROM collaboration_artifact_versions WHERE artifact_id=? AND version=?`, artifactID, versionNumber).
		Scan(&sourceExecution, &sourceAttempt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if sourceAttempt != "" {
		if sourceAttempt == in.AttemptID && sourceExecution == in.ExecutionID {
			return nil
		}
		return ErrConflict
	}
	if sourceExecution != "" && sourceExecution != in.ExecutionID {
		return ErrConflict
	}

	var valid int
	if err = s.db.QueryRowContext(ctx, `SELECT 1 FROM execution_attempts
		WHERE id=? AND execution_id=?`, in.AttemptID, in.ExecutionID).Scan(&valid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return err
	}

	result, err := s.db.ExecContext(ctx, `UPDATE collaboration_artifact_versions
		SET source_execution_id=?,source_attempt_id=?
		WHERE artifact_id=? AND version=?
		  AND COALESCE(source_attempt_id,'')=''
		  AND COALESCE(source_execution_id,'') IN ('',?)`,
		in.ExecutionID, in.AttemptID, artifactID, versionNumber, in.ExecutionID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

// CreateArtifactEvidenceWithProvenance creates version-scoped evidence and
// validates its optional Execution/Attempt lineage before persisting it.
func (s *Service) CreateArtifactEvidenceWithProvenance(ctx context.Context, artifactID string, in CreateArtifactEvidenceProvenanceRequest) (ArtifactProvenanceEvidence, error) {
	artifactID = strings.TrimSpace(artifactID)
	in.VersionID = strings.TrimSpace(in.VersionID)
	in.Kind = strings.TrimSpace(in.Kind)
	in.SourceRef = strings.TrimSpace(in.SourceRef)
	in.ExecutionID = strings.TrimSpace(in.ExecutionID)
	in.AttemptID = strings.TrimSpace(in.AttemptID)
	if artifactID == "" || in.VersionID == "" || in.Kind == "" ||
		len(in.SourceRef) > 2048 || len(in.Statement) > 4000 ||
		len(in.ContentSHA256) > 64 || len(in.CreatedBy) > 128 ||
		(in.AttemptID != "" && in.ExecutionID == "") {
		return ArtifactProvenanceEvidence{}, ErrInvalid
	}
	switch in.Kind {
	case "test", "build", "diff", "source_reference", "tool_log", "metric", "manual":
	default:
		return ArtifactProvenanceEvidence{}, ErrInvalid
	}

	var versionSourceExecution, digest string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(source_execution_id,''),content_sha256
		FROM collaboration_artifact_versions WHERE artifact_id=? AND id=?`, artifactID, in.VersionID).
		Scan(&versionSourceExecution, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactProvenanceEvidence{}, ErrNotFound
	}
	if err != nil {
		return ArtifactProvenanceEvidence{}, err
	}
	if in.ContentSHA256 != "" && !strings.EqualFold(in.ContentSHA256, digest) {
		return ArtifactProvenanceEvidence{}, ErrConflict
	}
	if versionSourceExecution != "" && in.ExecutionID != "" && versionSourceExecution != in.ExecutionID {
		return ArtifactProvenanceEvidence{}, ErrConflict
	}
	if in.ExecutionID != "" {
		var exists int
		if err = s.db.QueryRowContext(ctx, "SELECT 1 FROM executions WHERE id=?", in.ExecutionID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ArtifactProvenanceEvidence{}, ErrConflict
			}
			return ArtifactProvenanceEvidence{}, err
		}
	}
	if in.AttemptID != "" {
		var exists int
		if err = s.db.QueryRowContext(ctx, `SELECT 1 FROM execution_attempts
			WHERE id=? AND execution_id=?`, in.AttemptID, in.ExecutionID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ArtifactProvenanceEvidence{}, ErrConflict
			}
			return ArtifactProvenanceEvidence{}, err
		}
	}

	id, err := newID()
	if err != nil {
		return ArtifactProvenanceEvidence{}, err
	}
	now := s.clock().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO collaboration_artifact_evidence
		(id,artifact_id,version_id,workspace_id,task_id,kind,source_ref,content_sha256,statement,created_by,created_at,execution_id,attempt_id)
		SELECT ?,a.id,v.id,a.workspace_id,a.source_task_id,?,?,?,?,?,?,?,?
		FROM collaboration_artifacts a
		JOIN collaboration_artifact_versions v ON v.artifact_id=a.id
		WHERE a.id=? AND v.id=?`,
		id, in.Kind, in.SourceRef, in.ContentSHA256, in.Statement, in.CreatedBy,
		now.UnixMilli(), null(in.ExecutionID), null(in.AttemptID), artifactID, in.VersionID)
	if err != nil {
		return ArtifactProvenanceEvidence{}, err
	}
	return ArtifactProvenanceEvidence{
		ID: id, VersionID: in.VersionID, Kind: in.Kind, SourceRef: in.SourceRef,
		ExecutionID: in.ExecutionID, AttemptID: in.AttemptID, CreatedAt: now,
	}, nil
}

// GetArtifactProvenance returns the version/evidence lineage without copying
// Artifact content or duplicating the P3 verification/review stores.
func (s *Service) GetArtifactProvenance(ctx context.Context, artifactID string) (ArtifactProvenance, error) {
	if _, err := s.GetArtifact(ctx, artifactID); err != nil {
		return ArtifactProvenance{}, err
	}
	out := ArtifactProvenance{
		ArtifactID: artifactID,
		Versions:   make([]ArtifactProvenanceVersion, 0),
		Evidence:   make([]ArtifactProvenanceEvidence, 0),
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,version,COALESCE(source_execution_id,''),COALESCE(source_attempt_id,''),created_at
		FROM collaboration_artifact_versions WHERE artifact_id=? ORDER BY version`, artifactID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item ArtifactProvenanceVersion
		var created int64
		if err = rows.Scan(&item.ID, &item.Version, &item.SourceExecutionID, &item.SourceAttemptID, &created); err != nil {
			rows.Close()
			return out, err
		}
		item.CreatedAt = time.UnixMilli(created).UTC()
		out.Versions = append(out.Versions, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT id,version_id,kind,source_ref,COALESCE(execution_id,''),COALESCE(attempt_id,''),created_at
		FROM collaboration_artifact_evidence WHERE artifact_id=? ORDER BY created_at,id`, artifactID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ArtifactProvenanceEvidence
		var created int64
		if err = rows.Scan(&item.ID, &item.VersionID, &item.Kind, &item.SourceRef, &item.ExecutionID, &item.AttemptID, &created); err != nil {
			return out, err
		}
		item.CreatedAt = time.UnixMilli(created).UTC()
		out.Evidence = append(out.Evidence, item)
	}
	return out, rows.Err()
}
