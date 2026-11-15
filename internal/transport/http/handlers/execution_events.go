package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// ExecutionEvent is an immutable fact emitted by the SQLite execution ledger.
// Sequence, not occurred_at, is the authoritative ordering key.
type ExecutionEvent struct {
	Sequence    int64          `json:"sequence"`
	EventID     string         `json:"event_id"`
	EventKey    string         `json:"event_key"`
	ExecutionID string         `json:"execution_id"`
	AttemptID   string         `json:"attempt_id,omitempty"`
	Type        string         `json:"type"`
	ActorType   string         `json:"actor_type"`
	ActorID     string         `json:"actor_id,omitempty"`
	Source      string         `json:"source"`
	Payload     json.RawMessage `json:"payload"`
	OccurredAt  time.Time      `json:"occurred_at"`
}

type ExecutionEventsHandler struct{ db *sql.DB }

func NewExecutionEventsHandler(db *sql.DB) *ExecutionEventsHandler {
	return &ExecutionEventsHandler{db: db}
}

func (h *ExecutionEventsHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/managed-executions/:id/events", h.list)
}

func (h *ExecutionEventsHandler) list(c *gin.Context) {
	executionID := c.Param("id")
	if executionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_execution_id"})
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
	after, err := strconv.ParseInt(c.DefaultQuery("after_sequence", "0"), 10, 64)
	if err != nil || after < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_after_sequence"})
		return
	}

	var exists int
	if err = h.db.QueryRowContext(c.Request.Context(), "SELECT 1 FROM executions WHERE id=?", executionID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"code": "execution_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_events_read_failed"})
		return
	}

	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT sequence,event_id,event_key,execution_id,COALESCE(attempt_id,''),
		       event_type,actor_type,actor_id,source,payload_json,occurred_at
		FROM execution_events
		WHERE execution_id=? AND sequence>?
		ORDER BY sequence ASC
		LIMIT ?`, executionID, after, limit+1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_events_read_failed"})
		return
	}
	defer rows.Close()

	items := make([]ExecutionEvent, 0, limit)
	for rows.Next() {
		var item ExecutionEvent
		var occurredAt int64
		var payload []byte
		if err = rows.Scan(&item.Sequence, &item.EventID, &item.EventKey, &item.ExecutionID,
			&item.AttemptID, &item.Type, &item.ActorType, &item.ActorID, &item.Source,
			&payload, &occurredAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_events_read_failed"})
			return
		}
		item.Payload = append(json.RawMessage(nil), payload...)
		item.OccurredAt = time.Unix(0, occurredAt).UTC()
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "execution_events_read_failed"})
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	next := after
	if len(items) > 0 {
		next = items[len(items)-1].Sequence
	}
	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"limit": limit,
		"after_sequence": after,
		"next_sequence": next,
		"has_more": hasMore,
	})
}
