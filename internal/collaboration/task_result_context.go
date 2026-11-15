package collaboration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const (
	maxEvaluationArtifacts      = 8
	maxEvaluationArtifactBytes  = 4 << 20
	maxEvaluationEvidence       = 50
	maxEvaluationVerifications  = 5
)

// TaskArtifactContext is a bounded, immutable snapshot of one deliverable version.
// Content may be omitted when the aggregate context byte budget is exhausted;
// its digest and metadata remain available to the evaluator.
type TaskArtifactContext struct {
	Artifact        Artifact               `json:"artifact"`
	Version         ArtifactVersion        `json:"version"`
	ContentIncluded bool                   `json:"content_included"`
	Evidence        []ArtifactEvidence     `json:"evidence"`
	Verifications   []ArtifactVerification `json:"verifications"`
}

// TaskResultEvaluationContext joins a completed result with the deliverables
// explicitly attached to its Task. It is a snapshot, not a mutable live view.
type TaskResultEvaluationContext struct {
	Task                  Task                  `json:"task"`
	Result                TaskResult            `json:"result"`
	Artifacts             []TaskArtifactContext `json:"artifacts"`
	ArtifactContentBytes  int                   `json:"artifact_content_bytes"`
	ArtifactsTruncated    bool                  `json:"artifacts_truncated"`
}

// ContextualTaskResultEvaluator can inspect task-linked artifacts, exact
// immutable versions, evidence and deterministic verification history.
type ContextualTaskResultEvaluator interface {
	TaskResultEvaluator
	EvaluateTaskResultWithContext(context.Context, TaskResultEvaluationContext) (TaskResultAssessment, error)
}

// BuildTaskResultEvaluationContext constructs a bounded evaluation snapshot.
// Only artifacts explicitly linked through source_task_id are included.
func (s *Service) BuildTaskResultEvaluationContext(ctx context.Context, recipientAgentID, assignmentID string) (TaskResultEvaluationContext, error) {
	result, err := s.GetTaskResult(ctx, recipientAgentID, assignmentID)
	if err != nil {
		return TaskResultEvaluationContext{}, err
	}
	task, err := s.GetTask(ctx, result.TaskID)
	if err != nil {
		return TaskResultEvaluationContext{}, err
	}
	if task.WorkspaceID != result.WorkspaceID {
		return TaskResultEvaluationContext{}, ErrConflict
	}
	out := TaskResultEvaluationContext{Task: task, Result: result, Artifacts: make([]TaskArtifactContext, 0)}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM collaboration_artifacts
		WHERE workspace_id=? AND source_task_id=? ORDER BY updated_at DESC,id LIMIT ?`,
		task.WorkspaceID, task.ID, maxEvaluationArtifacts+1)
	if err != nil {
		return TaskResultEvaluationContext{}, err
	}
	artifactIDs := make([]string, 0, maxEvaluationArtifacts+1)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return TaskResultEvaluationContext{}, err
		}
		artifactIDs = append(artifactIDs, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return TaskResultEvaluationContext{}, err
	}
	if err = rows.Close(); err != nil {
		return TaskResultEvaluationContext{}, err
	}
	if len(artifactIDs) > maxEvaluationArtifacts {
		out.ArtifactsTruncated = true
		artifactIDs = artifactIDs[:maxEvaluationArtifacts]
	}
	for _, artifactID := range artifactIDs {
		artifact, readErr := s.GetArtifact(ctx, artifactID)
		if readErr != nil {
			return TaskResultEvaluationContext{}, readErr
		}
		version, readErr := s.GetArtifactVersion(ctx, artifactID, artifact.CurrentVersion)
		if readErr != nil {
			return TaskResultEvaluationContext{}, readErr
		}
		contentIncluded := len(version.Content) <= maxEvaluationArtifactBytes-out.ArtifactContentBytes
		if contentIncluded {
			out.ArtifactContentBytes += len(version.Content)
		} else {
			version.Content = nil
		}
		evidence, readErr := s.listArtifactEvidenceForVersion(ctx, artifactID, version.ID, maxEvaluationEvidence)
		if readErr != nil {
			return TaskResultEvaluationContext{}, readErr
		}
		verifications, readErr := s.listArtifactVerificationsForVersion(ctx, artifactID, version.ID, maxEvaluationVerifications)
		if readErr != nil {
			return TaskResultEvaluationContext{}, readErr
		}
		out.Artifacts = append(out.Artifacts, TaskArtifactContext{
			Artifact: artifact, Version: version, ContentIncluded: contentIncluded,
			Evidence: evidence, Verifications: verifications,
		})
	}
	return out, nil
}

func (s *Service) listArtifactEvidenceForVersion(ctx context.Context, artifactID, versionID string, limit int) ([]ArtifactEvidence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,artifact_id,version_id,workspace_id,COALESCE(task_id,''),kind,source_ref,content_sha256,statement,created_by,created_at
		FROM collaboration_artifact_evidence WHERE artifact_id=? AND version_id=? ORDER BY created_at DESC,id DESC LIMIT ?`,
		artifactID, versionID, limit)
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

func (s *Service) listArtifactVerificationsForVersion(ctx context.Context, artifactID, versionID string, limit int) ([]ArtifactVerification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,artifact_id,version_id,outcome,policy_json,evidence_summary_json,reason,created_at
		FROM collaboration_artifact_verifications WHERE artifact_id=? AND version_id=? ORDER BY created_at DESC,id DESC LIMIT ?`,
		artifactID, versionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ArtifactVerification, 0)
	for rows.Next() {
		var item ArtifactVerification
		var policy, summary string
		var created int64
		if err = rows.Scan(&item.ID, &item.ArtifactID, &item.VersionID, &item.Outcome, &policy, &summary, &item.Reason, &created); err != nil {
			return nil, err
		}
		item.Policy = json.RawMessage(policy)
		item.EvidenceSummary = json.RawMessage(summary)
		item.CreatedAt = time.UnixMilli(created).UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

// Keep the compiler honest if a future schema change removes nullable result links.
var _ = sql.ErrNoRows
var _ = errors.Is
