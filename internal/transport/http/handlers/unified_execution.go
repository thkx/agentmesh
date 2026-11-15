package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// UnifiedExecutionHandler provides read-only cross-runtime views over the
// canonical Execution/Attempt tables and the existing P3 Artifact/Evidence
// stores. It deliberately does not create a second execution repository.
type UnifiedExecutionHandler struct{ db *sql.DB }

func NewUnifiedExecutionHandler(db *sql.DB) *UnifiedExecutionHandler {
	return &UnifiedExecutionHandler{db: db}
}

func (h *UnifiedExecutionHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/managed-executions/:id/detail", h.detail)
	r.GET("/managed-executions/:id/timeline", h.timeline)
}

type unifiedExecution struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	Runtime        string         `json:"runtime"`
	RuntimeRef     string         `json:"runtime_ref,omitempty"`
	TaskID         string         `json:"task_id,omitempty"`
	Status         string         `json:"status"`
	Input          jsonRawMessage `json:"input,omitempty"`
	Result         jsonRawMessage `json:"result,omitempty"`
	Error          string         `json:"error,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	StartedAt      *time.Time     `json:"started_at,omitempty"`
	FinishedAt     *time.Time     `json:"finished_at,omitempty"`
}

// jsonRawMessage avoids exposing SQL's nullable JSON text as a quoted string.
type jsonRawMessage = json.RawMessage

type unifiedAttempt struct {
	ID         string         `json:"id"`
	Number     int            `json:"number"`
	Runtime    string         `json:"runtime"`
	RuntimeRef string         `json:"runtime_ref,omitempty"`
	Status     string         `json:"status"`
	Input      jsonRawMessage `json:"input,omitempty"`
	Result     jsonRawMessage `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
}

type unifiedArtifactVersion struct {
	ID                string    `json:"id"`
	ArtifactID        string    `json:"artifact_id"`
	Version           int       `json:"version"`
	SourceExecutionID string    `json:"source_execution_id,omitempty"`
	SourceAttemptID   string    `json:"source_attempt_id,omitempty"`
	ContentSHA256     string    `json:"content_sha256,omitempty"`
	MediaType         string    `json:"media_type,omitempty"`
	SizeBytes         int64     `json:"size_bytes"`
	CreatedAt         time.Time `json:"created_at"`
}

type unifiedEvidence struct {
	ID          string    `json:"id"`
	ArtifactID  string    `json:"artifact_id"`
	VersionID   string    `json:"version_id"`
	Kind        string    `json:"kind"`
	SourceRef   string    `json:"source_ref,omitempty"`
	Statement   string    `json:"statement,omitempty"`
	ExecutionID string    `json:"execution_id,omitempty"`
	AttemptID   string    `json:"attempt_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (h *UnifiedExecutionHandler) detail(c *gin.Context) {
	id := c.Param("id")
	var e unifiedExecution
	var runtimeRef, taskID, idem sql.NullString
	var input, result sql.NullString
	var started, finished sql.NullInt64
	var created, updated int64
	err := h.db.QueryRowContext(c.Request.Context(), `
		SELECT id,kind,runtime,runtime_ref,task_id,status,input_json,result_json,error_text,
		       idempotency_key,created_at,updated_at,started_at,finished_at
		FROM executions WHERE id=?`, id).Scan(
		&e.ID, &e.Kind, &e.Runtime, &runtimeRef, &taskID, &e.Status, &input, &result,
		&e.Error, &idem, &created, &updated, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"code": "execution_not_found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_detail_read_failed"})
		return
	}
	e.RuntimeRef, e.TaskID, e.IdempotencyKey = runtimeRef.String, taskID.String, idem.String
	e.Input, e.Result = nullableRawJSON(input), nullableRawJSON(result)
	e.CreatedAt, e.UpdatedAt = time.Unix(0, created).UTC(), time.Unix(0, updated).UTC()
	e.StartedAt, e.FinishedAt = nullableNanoTime(started), nullableNanoTime(finished)

	attempts := make([]unifiedAttempt, 0)
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT id,attempt_number,runtime,runtime_ref,status,input_json,result_json,error_text,
		       started_at,finished_at
		FROM execution_attempts WHERE execution_id=? ORDER BY attempt_number`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_attempts_read_failed"})
		return
	}
	for rows.Next() {
		var a unifiedAttempt
		var ref, in, out sql.NullString
		var ended sql.NullInt64
		var began int64
		if err = rows.Scan(&a.ID, &a.Number, &a.Runtime, &ref, &a.Status, &in, &out,
			&a.Error, &began, &ended); err != nil {
			rows.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_attempts_read_failed"})
			return
		}
		a.RuntimeRef, a.Input, a.Result = ref.String, nullableRawJSON(in), nullableRawJSON(out)
		a.StartedAt, a.FinishedAt = time.Unix(0, began).UTC(), nullableNanoTime(ended)
		attempts = append(attempts, a)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_attempts_read_failed"})
		return
	}
	if err = rows.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_attempts_read_failed"})
		return
	}

	versions := make([]unifiedArtifactVersion, 0)
	rows, err = h.db.QueryContext(c.Request.Context(), `
		SELECT id,artifact_id,version,COALESCE(source_execution_id,''),
		       COALESCE(source_attempt_id,''),COALESCE(content_sha256,''),
		       COALESCE(media_type,''),COALESCE(size_bytes,0),created_at
		FROM collaboration_artifact_versions
		WHERE source_execution_id=? OR source_attempt_id IN
		      (SELECT id FROM execution_attempts WHERE execution_id=?)
		ORDER BY artifact_id,version`, id, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_artifacts_read_failed"})
		return
	}
	for rows.Next() {
		var v unifiedArtifactVersion
		var createdMS int64
		if err = rows.Scan(&v.ID, &v.ArtifactID, &v.Version, &v.SourceExecutionID,
			&v.SourceAttemptID, &v.ContentSHA256, &v.MediaType, &v.SizeBytes, &createdMS); err != nil {
			rows.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_artifacts_read_failed"})
			return
		}
		v.CreatedAt = time.UnixMilli(createdMS).UTC()
		versions = append(versions, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_artifacts_read_failed"})
		return
	}
	if err = rows.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_artifacts_read_failed"})
		return
	}

	evidence := make([]unifiedEvidence, 0)
	rows, err = h.db.QueryContext(c.Request.Context(), `
		SELECT id,artifact_id,version_id,kind,COALESCE(source_ref,''),
		       COALESCE(statement,''),COALESCE(execution_id,''),COALESCE(attempt_id,''),created_at
		FROM collaboration_artifact_evidence
		WHERE execution_id=? OR attempt_id IN
		      (SELECT id FROM execution_attempts WHERE execution_id=?)
		ORDER BY created_at,id`, id, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_evidence_read_failed"})
		return
	}
	for rows.Next() {
		var item unifiedEvidence
		var createdMS int64
		if err = rows.Scan(&item.ID, &item.ArtifactID, &item.VersionID, &item.Kind,
			&item.SourceRef, &item.Statement, &item.ExecutionID, &item.AttemptID, &createdMS); err != nil {
			rows.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_evidence_read_failed"})
			return
		}
		item.CreatedAt = time.UnixMilli(createdMS).UTC()
		evidence = append(evidence, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_evidence_read_failed"})
		return
	}
	if err = rows.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_evidence_read_failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"execution":         e,
		"attempts":          attempts,
		"artifact_versions": versions,
		"evidence":          evidence,
	})
}

type unifiedTimelineEvent struct {
	OccurredAt time.Time      `json:"occurred_at"`
	Stream     string         `json:"stream"`
	Sequence   int64          `json:"sequence"`
	EventID    string         `json:"event_id"`
	EventKey   string         `json:"event_key"`
	Type       string         `json:"type"`
	ActorType  string         `json:"actor_type"`
	ActorID    string         `json:"actor_id,omitempty"`
	Source     string         `json:"source"`
	AttemptID  string         `json:"attempt_id,omitempty"`
	Payload    jsonRawMessage `json:"payload"`
}

func (h *UnifiedExecutionHandler) timeline(c *gin.Context) {
	id := c.Param("id")
	var exists int
	if err := h.db.QueryRowContext(c.Request.Context(), "SELECT 1 FROM executions WHERE id=?", id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"code": "execution_not_found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_timeline_read_failed"})
		}
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_limit", "message": "limit must be between 1 and 200"})
		return
	}
	if limit > 200 {
		limit = 200
	}
	afterTime, err := strconv.ParseInt(c.DefaultQuery("after_time_ns", "0"), 10, 64)
	if err != nil || afterTime < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_after_time"})
		return
	}
	afterStream := c.DefaultQuery("after_stream", "")
	afterSequence, err := strconv.ParseInt(c.DefaultQuery("after_sequence", "0"), 10, 64)
	if err != nil || afterSequence < 0 || (afterStream != "" && afterStream != "lifecycle" && afterStream != "control") {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_timeline_cursor"})
		return
	}

	events := make([]unifiedTimelineEvent, 0, 64)
	if err = h.readTimelineStream(c, id, "lifecycle", afterTime, afterStream, afterSequence, &events); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_timeline_read_failed"})
		return
	}
	if err = h.readTimelineStream(c, id, "control", afterTime, afterStream, afterSequence, &events); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_timeline_read_failed"})
		return
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].OccurredAt.Equal(events[j].OccurredAt) {
			if events[i].Stream == events[j].Stream {
				return events[i].Sequence < events[j].Sequence
			}
			return events[i].Stream < events[j].Stream
		}
		return events[i].OccurredAt.Before(events[j].OccurredAt)
	})
	// The cursor is a tuple because each append-only stream has its own sequence.
	filtered := events[:0]
	for _, event := range events {
		if event.OccurredAt.UnixNano() < afterTime {
			continue
		}
		if event.OccurredAt.UnixNano() == afterTime && afterStream != "" {
			if event.Stream < afterStream || (event.Stream == afterStream && event.Sequence <= afterSequence) {
				continue
			}
		}
		filtered = append(filtered, event)
	}
	events = filtered
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	nextTime, nextStream, nextSequence := afterTime, afterStream, afterSequence
	if len(events) > 0 {
		last := events[len(events)-1]
		nextTime, nextStream, nextSequence = last.OccurredAt.UnixNano(), last.Stream, last.Sequence
	}
	c.JSON(http.StatusOK, gin.H{
		"items": events, "limit": limit, "has_more": hasMore,
		"next_cursor": gin.H{"after_time_ns": nextTime, "after_stream": nextStream, "after_sequence": nextSequence},
		"ordering":    "occurred_at_utc_then_stream_then_stream_sequence",
	})
}

func (h *UnifiedExecutionHandler) readTimelineStream(c *gin.Context, id, stream string, after int64, afterStream string, afterSequence int64, out *[]unifiedTimelineEvent) error {
	table := "execution_events"
	if stream == "control" {
		table = "execution_control_events"
	}
	query := "SELECT sequence,event_id,event_key,event_type,actor_type,actor_id,source,payload_json,occurred_at"
	if stream == "lifecycle" {
		query += ",COALESCE(attempt_id,'')"
	} else {
		query += ",''"
	}
	query += " FROM " + table + " WHERE execution_id=?"
	args := []any{id}
	switch {
	case afterStream == "":
		query += " AND occurred_at>=?"
		args = append(args, after)
	case stream > afterStream:
		// This stream sorts after the cursor stream, so all events at the
		// cursor timestamp are still ahead of the cursor.
		query += " AND occurred_at>=?"
		args = append(args, after)
	case stream == afterStream:
		query += " AND (occurred_at>? OR (occurred_at=? AND sequence>?))"
		args = append(args, after, after, afterSequence)
	default:
		query += " AND occurred_at>?"
		args = append(args, after)
	}
	query += " ORDER BY occurred_at,sequence LIMIT 1001"
	rows, err := h.db.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item unifiedTimelineEvent
		var occurred int64
		item.Stream = stream
		if err = rows.Scan(&item.Sequence, &item.EventID, &item.EventKey, &item.Type,
			&item.ActorType, &item.ActorID, &item.Source, &item.Payload, &occurred, &item.AttemptID); err != nil {
			return err
		}
		item.OccurredAt = time.Unix(0, occurred).UTC()
		item.Payload = append(jsonRawMessage(nil), item.Payload...)
		*out = append(*out, item)
	}
	return rows.Err()
}

func nullableNanoTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := time.Unix(0, value.Int64).UTC()
	return &t
}

func nullableRawJSON(value sql.NullString) jsonRawMessage {
	if !value.Valid || value.String == "" {
		return nil
	}
	if !json.Valid([]byte(value.String)) {
		return nil
	}
	return jsonRawMessage(append([]byte(nil), value.String...))
}
