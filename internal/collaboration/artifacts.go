package collaboration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const MaxArtifactBytes = 2 << 20

type Artifact struct {
	ID                 string    `json:"id"`
	WorkspaceID        string    `json:"workspace_id"`
	SourceTaskID       string    `json:"source_task_id,omitempty"`
	CreatorAgentID     string    `json:"creator_agent_id,omitempty"`
	Kind               string    `json:"kind"`
	Title              string    `json:"title"`
	Status             string    `json:"status"`
	CurrentVersion     int       `json:"current_version"`
	SubmittedVersionID string    `json:"submitted_version_id,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type ArtifactVersion struct {
	ID              string          `json:"id"`
	ArtifactID      string          `json:"artifact_id"`
	Version         int             `json:"version"`
	Content         []byte          `json:"content,omitempty"`
	ContentSHA256   string          `json:"content_sha256"`
	MediaType       string          `json:"media_type"`
	SizeBytes       int64           `json:"size_bytes"`
	Metadata        json.RawMessage `json:"metadata"`
	CreatorAgentID  string          `json:"creator_agent_id,omitempty"`
	SourceExecution string          `json:"source_execution_id,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

type ArtifactEvidence struct {
	ID            string    `json:"id"`
	ArtifactID    string    `json:"artifact_id"`
	VersionID     string    `json:"version_id"`
	WorkspaceID   string    `json:"workspace_id"`
	TaskID        string    `json:"task_id,omitempty"`
	Kind          string    `json:"kind"`
	SourceRef     string    `json:"source_ref,omitempty"`
	ContentSHA256 string    `json:"content_sha256,omitempty"`
	Statement     string    `json:"statement,omitempty"`
	CreatedBy     string    `json:"created_by,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type ArtifactVerificationPolicy struct {
	RequiredEvidenceKinds []string `json:"required_evidence_kinds"`
	RequireContentDigest  bool     `json:"require_content_digest"`
}

type ArtifactVerification struct {
	ID              string          `json:"id"`
	ArtifactID      string          `json:"artifact_id"`
	VersionID       string          `json:"version_id"`
	Outcome         string          `json:"outcome"`
	Policy          json.RawMessage `json:"policy"`
	EvidenceSummary json.RawMessage `json:"evidence_summary"`
	Reason          string          `json:"reason"`
	CreatedAt       time.Time       `json:"created_at"`
}

type CreateArtifactRequest struct {
	WorkspaceID     string          `json:"workspace_id"`
	SourceTaskID    string          `json:"source_task_id"`
	CreatorAgentID  string          `json:"creator_agent_id"`
	Kind            string          `json:"kind"`
	Title           string          `json:"title"`
	Content         []byte          `json:"content"`
	MediaType       string          `json:"media_type"`
	Metadata        json.RawMessage `json:"metadata"`
	SourceExecution string          `json:"source_execution_id"`
}

type AddArtifactEvidenceRequest struct {
	VersionID     string `json:"version_id"`
	Kind          string `json:"kind"`
	SourceRef     string `json:"source_ref"`
	ContentSHA256 string `json:"content_sha256"`
	Statement     string `json:"statement"`
	CreatedBy     string `json:"created_by"`
}

func (s *Service) CreateArtifact(ctx context.Context, in CreateArtifactRequest) (Artifact, ArtifactVersion, error) {
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	in.Kind = strings.TrimSpace(in.Kind)
	in.Title = strings.TrimSpace(in.Title)
	in.CreatorAgentID = strings.TrimSpace(in.CreatorAgentID)
	in.SourceTaskID = strings.TrimSpace(in.SourceTaskID)
	if in.WorkspaceID == "" || in.Kind == "" || len(in.Kind) > 64 ||
		in.Title == "" || len(in.Title) > 240 || len(in.Content) == 0 ||
		len(in.Content) > MaxArtifactBytes {
		return Artifact{}, ArtifactVersion{}, ErrInvalid
	}
	if in.MediaType == "" {
		in.MediaType = "application/octet-stream"
	}
	if len(in.MediaType) > 128 {
		return Artifact{}, ArtifactVersion{}, ErrInvalid
	}
	if len(in.Metadata) == 0 {
		in.Metadata = json.RawMessage("{}")
	}
	if !json.Valid(in.Metadata) || len(in.Metadata) > 64<<10 {
		return Artifact{}, ArtifactVersion{}, ErrInvalid
	}
	if _, err := s.GetWorkspace(ctx, in.WorkspaceID); err != nil {
		return Artifact{}, ArtifactVersion{}, err
	}
	if in.SourceTaskID != "" {
		task, err := s.GetTask(ctx, in.SourceTaskID)
		if err != nil {
			return Artifact{}, ArtifactVersion{}, err
		}
		if task.WorkspaceID != in.WorkspaceID {
			return Artifact{}, ArtifactVersion{}, ErrConflict
		}
	}
	if in.CreatorAgentID != "" {
		agent, err := s.GetAgent(ctx, in.CreatorAgentID)
		if err != nil {
			return Artifact{}, ArtifactVersion{}, err
		}
		if agent.WorkspaceID != in.WorkspaceID {
			return Artifact{}, ArtifactVersion{}, ErrConflict
		}
	}

	artifactID, err := newID()
	if err != nil {
		return Artifact{}, ArtifactVersion{}, err
	}
	versionID, err := newID()
	if err != nil {
		return Artifact{}, ArtifactVersion{}, err
	}
	now := s.clock().UTC()
	sum := sha256.Sum256(in.Content)
	artifact := Artifact{
		ID: artifactID, WorkspaceID: in.WorkspaceID, SourceTaskID: in.SourceTaskID,
		CreatorAgentID: in.CreatorAgentID, Kind: in.Kind, Title: in.Title,
		Status: "draft", CurrentVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	version := ArtifactVersion{
		ID: versionID, ArtifactID: artifactID, Version: 1, Content: in.Content,
		ContentSHA256: hex.EncodeToString(sum[:]), MediaType: in.MediaType,
		SizeBytes: int64(len(in.Content)), Metadata: in.Metadata,
		CreatorAgentID: in.CreatorAgentID, SourceExecution: in.SourceExecution,
		CreatedAt: now,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return artifact, version, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO collaboration_artifacts
		(id,workspace_id,source_task_id,creator_agent_id,kind,title,status,current_version,created_at,updated_at)
		VALUES(?,?,?,?,?,?,'draft',1,?,?)`,
		artifact.ID, artifact.WorkspaceID, null(artifact.SourceTaskID), null(artifact.CreatorAgentID),
		artifact.Kind, artifact.Title, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return artifact, version, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO collaboration_artifact_versions
		(id,artifact_id,version,content,content_sha256,media_type,size_bytes,metadata_json,creator_agent_id,source_execution_id,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		version.ID, version.ArtifactID, version.Version, version.Content, version.ContentSHA256,
		version.MediaType, version.SizeBytes, string(version.Metadata), null(version.CreatorAgentID),
		null(version.SourceExecution), now.UnixMilli())
	if err != nil {
		return artifact, version, err
	}
	if err = s.writeEventTx(ctx, tx, artifact.WorkspaceID, "artifact", artifact.ID, "artifact.created",
		map[string]any{"artifact_id": artifact.ID, "version_id": version.ID, "version": 1, "sha256": version.ContentSHA256}); err != nil {
		return artifact, version, err
	}
	return artifact, version, tx.Commit()
}

func (s *Service) AddArtifactVersion(ctx context.Context, artifactID string, in CreateArtifactRequest) (ArtifactVersion, error) {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" || len(in.Content) == 0 || len(in.Content) > MaxArtifactBytes {
		return ArtifactVersion{}, ErrInvalid
	}
	if len(in.Metadata) == 0 {
		in.Metadata = json.RawMessage("{}")
	}
	if !json.Valid(in.Metadata) || len(in.Metadata) > 64<<10 {
		return ArtifactVersion{}, ErrInvalid
	}
	if in.MediaType == "" {
		in.MediaType = "application/octet-stream"
	}
	if len(in.MediaType) > 128 {
		return ArtifactVersion{}, ErrInvalid
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ArtifactVersion{}, err
	}
	defer tx.Rollback()
	var workspaceID, creatorID string
	var current int
	err = tx.QueryRowContext(ctx, "SELECT workspace_id,COALESCE(creator_agent_id,''),current_version FROM collaboration_artifacts WHERE id=?", artifactID).
		Scan(&workspaceID, &creatorID, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactVersion{}, ErrNotFound
	}
	if err != nil {
		return ArtifactVersion{}, err
	}
	if in.CreatorAgentID != "" {
		agent, agentErr := s.GetAgent(ctx, in.CreatorAgentID)
		if agentErr != nil {
			return ArtifactVersion{}, agentErr
		}
		if agent.WorkspaceID != workspaceID {
			return ArtifactVersion{}, ErrConflict
		}
		creatorID = in.CreatorAgentID
	}
	id, err := newID()
	if err != nil {
		return ArtifactVersion{}, err
	}
	now := s.clock().UTC()
	sum := sha256.Sum256(in.Content)
	version := ArtifactVersion{
		ID: id, ArtifactID: artifactID, Version: current + 1, Content: in.Content,
		ContentSHA256: hex.EncodeToString(sum[:]), MediaType: in.MediaType,
		SizeBytes: int64(len(in.Content)), Metadata: in.Metadata,
		CreatorAgentID: creatorID, SourceExecution: in.SourceExecution, CreatedAt: now,
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO collaboration_artifact_versions
		(id,artifact_id,version,content,content_sha256,media_type,size_bytes,metadata_json,creator_agent_id,source_execution_id,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		version.ID, version.ArtifactID, version.Version, version.Content, version.ContentSHA256,
		version.MediaType, version.SizeBytes, string(version.Metadata), null(version.CreatorAgentID),
		null(version.SourceExecution), now.UnixMilli())
	if err != nil {
		return ArtifactVersion{}, err
	}
	res, err := tx.ExecContext(ctx, "UPDATE collaboration_artifacts SET current_version=?,status='draft',submitted_version_id=NULL,updated_at=? WHERE id=? AND current_version=?",
		version.Version, now.UnixMilli(), artifactID, current)
	if err != nil {
		return ArtifactVersion{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return ArtifactVersion{}, err
	}
	if n != 1 {
		return ArtifactVersion{}, ErrConflict
	}
	if err = s.writeEventTx(ctx, tx, workspaceID, "artifact", artifactID, "artifact.version_created",
		map[string]any{"artifact_id": artifactID, "version_id": version.ID, "version": version.Version, "sha256": version.ContentSHA256}); err != nil {
		return ArtifactVersion{}, err
	}
	return version, tx.Commit()
}

func (s *Service) GetArtifact(ctx context.Context, id string) (Artifact, error) {
	var artifact Artifact
	var taskID, agentID sql.NullString
	var created, updated int64
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,source_task_id,creator_agent_id,kind,title,status,current_version,COALESCE(submitted_version_id,''),created_at,updated_at
		FROM collaboration_artifacts WHERE id=?`, id).Scan(
		&artifact.ID, &artifact.WorkspaceID, &taskID, &agentID, &artifact.Kind, &artifact.Title,
		&artifact.Status, &artifact.CurrentVersion, &artifact.SubmittedVersionID, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact, ErrNotFound
	}
	if err != nil {
		return artifact, err
	}
	artifact.SourceTaskID = taskID.String
	artifact.CreatorAgentID = agentID.String
	artifact.CreatedAt = time.UnixMilli(created).UTC()
	artifact.UpdatedAt = time.UnixMilli(updated).UTC()
	return artifact, nil
}

func (s *Service) GetArtifactVersion(ctx context.Context, artifactID string, versionNumber int) (ArtifactVersion, error) {
	var version ArtifactVersion
	var metadata string
	var creatorID, executionID sql.NullString
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id,artifact_id,version,content,content_sha256,media_type,size_bytes,metadata_json,creator_agent_id,source_execution_id,created_at
		FROM collaboration_artifact_versions WHERE artifact_id=? AND version=?`, artifactID, versionNumber).Scan(
		&version.ID, &version.ArtifactID, &version.Version, &version.Content, &version.ContentSHA256,
		&version.MediaType, &version.SizeBytes, &metadata, &creatorID, &executionID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return version, ErrNotFound
	}
	if err != nil {
		return version, err
	}
	version.Metadata = json.RawMessage(metadata)
	version.CreatorAgentID = creatorID.String
	version.SourceExecution = executionID.String
	version.CreatedAt = time.UnixMilli(created).UTC()
	sum := sha256.Sum256(version.Content)
	if hex.EncodeToString(sum[:]) != version.ContentSHA256 {
		return version, fmt.Errorf("artifact content digest mismatch")
	}
	return version, nil
}

func (s *Service) AddArtifactEvidence(ctx context.Context, artifactID string, in AddArtifactEvidenceRequest) (ArtifactEvidence, error) {
	artifactID = strings.TrimSpace(artifactID)
	in.VersionID = strings.TrimSpace(in.VersionID)
	in.Kind = strings.TrimSpace(in.Kind)
	in.SourceRef = strings.TrimSpace(in.SourceRef)
	in.Statement = strings.TrimSpace(in.Statement)
	validKinds := map[string]bool{
		"test": true, "build": true, "diff": true, "source_reference": true,
		"tool_log": true, "metric": true, "manual": true,
	}
	if artifactID == "" || in.VersionID == "" || !validKinds[in.Kind] ||
		len(in.SourceRef) > 2048 || len(in.Statement) > 4000 || len(in.ContentSHA256) > 64 {
		return ArtifactEvidence{}, ErrInvalid
	}
	var workspaceID, taskID string
	err := s.db.QueryRowContext(ctx, `SELECT a.workspace_id,COALESCE(a.source_task_id,'')
		FROM collaboration_artifacts a JOIN collaboration_artifact_versions v ON v.artifact_id=a.id
		WHERE a.id=? AND v.id=?`, artifactID, in.VersionID).Scan(&workspaceID, &taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactEvidence{}, ErrNotFound
	}
	if err != nil {
		return ArtifactEvidence{}, err
	}
	if in.ContentSHA256 != "" {
		var digest string
		if err = s.db.QueryRowContext(ctx, "SELECT content_sha256 FROM collaboration_artifact_versions WHERE id=?", in.VersionID).Scan(&digest); err != nil {
			return ArtifactEvidence{}, err
		}
		if !strings.EqualFold(in.ContentSHA256, digest) {
			return ArtifactEvidence{}, ErrConflict
		}
	}
	id, err := newID()
	if err != nil {
		return ArtifactEvidence{}, err
	}
	now := s.clock().UTC()
	evidence := ArtifactEvidence{
		ID: id, ArtifactID: artifactID, VersionID: in.VersionID, WorkspaceID: workspaceID,
		TaskID: taskID, Kind: in.Kind, SourceRef: in.SourceRef, ContentSHA256: in.ContentSHA256,
		Statement: in.Statement, CreatedBy: in.CreatedBy, CreatedAt: now,
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO collaboration_artifact_evidence
		(id,artifact_id,version_id,workspace_id,task_id,kind,source_ref,content_sha256,statement,created_by,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		evidence.ID, evidence.ArtifactID, evidence.VersionID, evidence.WorkspaceID, null(evidence.TaskID),
		evidence.Kind, evidence.SourceRef, evidence.ContentSHA256, evidence.Statement, evidence.CreatedBy, now.UnixMilli())
	return evidence, err
}

func (s *Service) ListArtifactEvidence(ctx context.Context, artifactID string) ([]ArtifactEvidence, error) {
	if _, err := s.GetArtifact(ctx, artifactID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,artifact_id,version_id,workspace_id,COALESCE(task_id,''),kind,source_ref,content_sha256,statement,created_by,created_at
		FROM collaboration_artifact_evidence WHERE artifact_id=? ORDER BY created_at,id`, artifactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ArtifactEvidence, 0)
	for rows.Next() {
		var item ArtifactEvidence
		var created int64
		if err = rows.Scan(&item.ID, &item.ArtifactID, &item.VersionID, &item.WorkspaceID, &item.TaskID,
			&item.Kind, &item.SourceRef, &item.ContentSHA256, &item.Statement, &item.CreatedBy, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = time.UnixMilli(created).UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) VerifyArtifact(ctx context.Context, artifactID string, versionNumber int, policy ArtifactVerificationPolicy) (ArtifactVerification, error) {
	artifact, err := s.GetArtifact(ctx, artifactID)
	if err != nil {
		return ArtifactVerification{}, err
	}
	if versionNumber <= 0 {
		versionNumber = artifact.CurrentVersion
	}
	version, err := s.GetArtifactVersion(ctx, artifactID, versionNumber)
	if err != nil {
		return ArtifactVerification{}, err
	}
	required := make([]string, 0, len(policy.RequiredEvidenceKinds))
	seen := map[string]bool{}
	allowed := map[string]bool{
		"test": true, "build": true, "diff": true, "source_reference": true,
		"tool_log": true, "metric": true, "manual": true,
	}
	for _, kind := range policy.RequiredEvidenceKinds {
		kind = strings.TrimSpace(kind)
		if !allowed[kind] || seen[kind] {
			return ArtifactVerification{}, ErrInvalid
		}
		seen[kind] = true
		required = append(required, kind)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT kind,COUNT(*) FROM collaboration_artifact_evidence WHERE artifact_id=? AND version_id=? GROUP BY kind", artifactID, version.ID)
	if err != nil {
		return ArtifactVerification{}, err
	}
	counts := map[string]int{}
	for rows.Next() {
		var kind string
		var count int
		if err = rows.Scan(&kind, &count); err != nil {
			rows.Close()
			return ArtifactVerification{}, err
		}
		counts[kind] = count
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return ArtifactVerification{}, err
	}
	rows.Close()

	outcome := "passed"
	missing := []string{}
	for _, kind := range required {
		if counts[kind] == 0 {
			missing = append(missing, kind)
		}
	}
	if len(missing) > 0 {
		outcome = "needs_evidence"
	}
	if policy.RequireContentDigest {
		sum := sha256.Sum256(version.Content)
		if hex.EncodeToString(sum[:]) != version.ContentSHA256 {
			outcome = "failed"
		}
	}
	policyJSON, _ := json.Marshal(policy)
	summary, _ := json.Marshal(map[string]any{
		"counts": counts, "required": required, "missing": missing, "version_sha256": version.ContentSHA256,
	})
	reason := "all configured deterministic evidence requirements are present"
	if len(missing) > 0 {
		reason = "required evidence is missing: " + strings.Join(missing, ", ")
	}
	if outcome == "failed" {
		reason = "artifact content digest verification failed"
	}
	id, err := newID()
	if err != nil {
		return ArtifactVerification{}, err
	}
	now := s.clock().UTC()
	result := ArtifactVerification{
		ID: id, ArtifactID: artifactID, VersionID: version.ID, Outcome: outcome,
		Policy: policyJSON, EvidenceSummary: summary, Reason: reason, CreatedAt: now,
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO collaboration_artifact_verifications
		(id,artifact_id,version_id,outcome,policy_json,evidence_summary_json,reason,created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		result.ID, result.ArtifactID, result.VersionID, result.Outcome, string(result.Policy),
		string(result.EvidenceSummary), result.Reason, now.UnixMilli())
	if err != nil {
		return ArtifactVerification{}, err
	}
	return result, nil
}

type ArtifactReview struct {
	ID          string    `json:"id"`
	ArtifactID  string    `json:"artifact_id"`
	VersionID   string    `json:"version_id"`
	WorkspaceID string    `json:"workspace_id"`
	ReviewerID  string    `json:"reviewer_id"`
	Outcome     string    `json:"outcome"`
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"created_at"`
}

// SubmitArtifact makes the current immutable version available for human review.
// Submission is blocked until a deterministic verification for that exact
// version has passed.
func (s *Service) SubmitArtifact(ctx context.Context, artifactID string) (Artifact, error) {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return Artifact{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Artifact{}, err
	}
	defer tx.Rollback()

	var workspaceID, status, submittedVersionID string
	var currentVersion int
	err = tx.QueryRowContext(ctx, `SELECT workspace_id,status,current_version,COALESCE(submitted_version_id,'')
		FROM collaboration_artifacts WHERE id=?`, artifactID).Scan(
		&workspaceID, &status, &currentVersion, &submittedVersionID)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, ErrNotFound
	}
	if err != nil {
		return Artifact{}, err
	}
	var versionID string
	err = tx.QueryRowContext(ctx, "SELECT id FROM collaboration_artifact_versions WHERE artifact_id=? AND version=?", artifactID, currentVersion).Scan(&versionID)
	if err != nil {
		return Artifact{}, err
	}
	if status == "submitted" && submittedVersionID == versionID {
		_ = tx.Rollback()
		return s.GetArtifact(ctx, artifactID)
	}
	if status != "draft" {
		return Artifact{}, ErrConflict
	}
	var verificationOutcome string
	err = tx.QueryRowContext(ctx, `SELECT outcome FROM collaboration_artifact_verifications
		WHERE artifact_id=? AND version_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, artifactID, versionID).Scan(&verificationOutcome)
	if errors.Is(err, sql.ErrNoRows) || verificationOutcome != "passed" {
		return Artifact{}, ErrConflict
	}
	now := s.clock().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE collaboration_artifacts
		SET status='submitted',submitted_version_id=?,updated_at=?
		WHERE id=? AND status='draft' AND current_version=?`, versionID, now.UnixMilli(), artifactID, currentVersion)
	if err != nil {
		return Artifact{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Artifact{}, err
	}
	if affected != 1 {
		return Artifact{}, ErrConflict
	}
	if err = s.writeEventTx(ctx, tx, workspaceID, "artifact", artifactID, "artifact.submitted",
		map[string]any{"artifact_id": artifactID, "version_id": versionID}); err != nil {
		return Artifact{}, err
	}
	if err = tx.Commit(); err != nil {
		return Artifact{}, err
	}
	return s.GetArtifact(ctx, artifactID)
}

// ReviewArtifact records one human decision for the exact submitted version.
// The unique artifact/version key and identical-request check make retries safe.
func (s *Service) ReviewArtifact(ctx context.Context, artifactID, reviewerID, outcome, note string) (ArtifactReview, error) {
	artifactID = strings.TrimSpace(artifactID)
	reviewerID = strings.TrimSpace(reviewerID)
	outcome = strings.TrimSpace(outcome)
	note = strings.TrimSpace(note)
	if artifactID == "" || reviewerID == "" || len(reviewerID) > 160 ||
		(outcome != "accepted" && outcome != "rejected") || len(note) > 4000 {
		return ArtifactReview{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ArtifactReview{}, err
	}
	defer tx.Rollback()

	var workspaceID, status, versionID string
	err = tx.QueryRowContext(ctx, `SELECT workspace_id,status,COALESCE(submitted_version_id,'')
		FROM collaboration_artifacts WHERE id=?`, artifactID).Scan(&workspaceID, &status, &versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactReview{}, ErrNotFound
	}
	if err != nil {
		return ArtifactReview{}, err
	}
	if status != "submitted" || versionID == "" {
		var existing ArtifactReview
		var created int64
		readErr := tx.QueryRowContext(ctx, `SELECT id,artifact_id,version_id,workspace_id,reviewer_id,outcome,note,created_at
			FROM collaboration_artifact_reviews WHERE artifact_id=? AND version_id=?`, artifactID, versionID).Scan(
			&existing.ID, &existing.ArtifactID, &existing.VersionID, &existing.WorkspaceID,
			&existing.ReviewerID, &existing.Outcome, &existing.Note, &created)
		if readErr == nil {
			existing.CreatedAt = time.UnixMilli(created).UTC()
			if existing.ReviewerID == reviewerID && existing.Outcome == outcome && existing.Note == note {
				return existing, nil
			}
			return ArtifactReview{}, ErrConflict
		}
		return ArtifactReview{}, ErrConflict
	}

	id, err := newID()
	if err != nil {
		return ArtifactReview{}, err
	}
	now := s.clock().UTC()
	review := ArtifactReview{
		ID: id, ArtifactID: artifactID, VersionID: versionID, WorkspaceID: workspaceID,
		ReviewerID: reviewerID, Outcome: outcome, Note: note, CreatedAt: now,
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO collaboration_artifact_reviews
		(id,artifact_id,version_id,workspace_id,reviewer_id,outcome,note,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, review.ID, review.ArtifactID, review.VersionID,
		review.WorkspaceID, review.ReviewerID, review.Outcome, review.Note, now.UnixMilli())
	if err != nil {
		return ArtifactReview{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE collaboration_artifacts SET status=?,updated_at=?
		WHERE id=? AND status='submitted' AND submitted_version_id=?`, outcome, now.UnixMilli(), artifactID, versionID)
	if err != nil {
		return ArtifactReview{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ArtifactReview{}, err
	}
	if affected != 1 {
		return ArtifactReview{}, ErrConflict
	}
	if err = s.writeEventTx(ctx, tx, workspaceID, "artifact", artifactID, "artifact.reviewed",
		map[string]any{"artifact_id": artifactID, "version_id": versionID, "reviewer_id": reviewerID, "outcome": outcome, "note": note}); err != nil {
		return ArtifactReview{}, err
	}
	if err = tx.Commit(); err != nil {
		return ArtifactReview{}, err
	}
	return review, nil
}
