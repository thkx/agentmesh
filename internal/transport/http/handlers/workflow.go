package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
	"net/http"
	"strconv"
	"time"
)

type WorkflowEngine interface {
	PutDefinition(context.Context, wf.Definition) error
	Definition(context.Context, string, int) (wf.Definition, error)
	Start(context.Context, string, string, map[string]any) (wf.Run, error)
	StartQueued(context.Context, string, string, map[string]any, int) (wf.Run, error)
	GetRun(context.Context, string) (wf.Run, error)
	ListRuns(context.Context, string, int) ([]wf.Run, error)
	Resume(context.Context, string, bool, string) (wf.Run, error)
	ResumeQueued(context.Context, string, bool, string, int) (wf.Run, error)
	Events(context.Context, string, int64, int) ([]wf.Event, error)
	NodeObservations(context.Context, string) ([]wf.NodeObservation, error)
	RecoverNode(context.Context, string, string, string, string, int) (wf.Run, error)
}
type WorkflowHandler struct {
	engine      WorkflowEngine
	maxAttempts int
}

func NewWorkflowHandler(e WorkflowEngine) *WorkflowHandler {
	return &WorkflowHandler{engine: e, maxAttempts: 3}
}
func (h *WorkflowHandler) SetMaxAttempts(n int) {
	if n > 0 && n <= 20 {
		h.maxAttempts = n
	}
}
func (h *WorkflowHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/workflows")
	g.PUT("/:id", h.put)
	g.GET("/:id", h.get)
	g.POST("/:id/runs", h.start)
	g.GET("/:id/runs", h.listRuns)
	g.GET("/runs/:run_id", h.getRun)
	g.POST("/runs/:run_id/approve", h.approve)
	g.POST("/runs/:run_id/reject", h.reject)
	g.GET("/runs/:run_id/events", h.events)
	g.GET("/runs/:run_id/events/stream", h.eventStream)
	g.GET("/runs/:run_id/history", h.history)
	g.GET("/runs/:run_id/nodes", h.nodes)
	g.POST("/runs/:run_id/nodes/:node_id/recover", h.recoverNode)
}
func (h *WorkflowHandler) put(c *gin.Context) {
	var d wf.Definition
	if e := c.ShouldBindJSON(&d); e != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	if d.ID != c.Param("id") {
		c.JSON(http.StatusBadRequest, gin.H{"code": "workflow_id_mismatch"})
		return
	}
	if e := h.engine.PutDefinition(c.Request.Context(), d); e != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_workflow_definition", "message": e.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}
func (h *WorkflowHandler) get(c *gin.Context) {
	version := 0
	if v := c.Query("version"); v != "" {
		if _, e := fmt.Sscanf(v, "%d", &version); e != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_version"})
			return
		}
	}
	d, e := h.engine.Definition(c.Request.Context(), c.Param("id"), version)
	if e != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "workflow_not_found"})
		return
	}
	c.JSON(http.StatusOK, d)
}
func (h *WorkflowHandler) start(c *gin.Context) {
	var in struct {
		RunID string         `json:"run_id"`
		Input map[string]any `json:"input"`
	}
	if e := c.ShouldBindJSON(&in); e != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	r, e := h.engine.StartQueued(c.Request.Context(), c.Param("id"), in.RunID, in.Input, h.maxAttempts)
	if e != nil {
		status := http.StatusBadRequest
		if errors.Is(e, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		c.JSON(status, gin.H{"code": "workflow_start_failed", "message": e.Error(), "run": r})
		return
	}
	status := http.StatusAccepted
	c.JSON(status, r)
}
func (h *WorkflowHandler) getRun(c *gin.Context) {
	r, e := h.engine.GetRun(c.Request.Context(), c.Param("run_id"))
	if e != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "workflow_run_not_found"})
		return
	}
	c.JSON(http.StatusOK, r)
}
func (h *WorkflowHandler) listRuns(c *gin.Context) {
	runs, e := h.engine.ListRuns(c.Request.Context(), c.Param("id"), 50)
	if e != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "workflow_runs_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": runs})
}
func (h *WorkflowHandler) approve(c *gin.Context) { h.decision(c, true) }
func (h *WorkflowHandler) reject(c *gin.Context)  { h.decision(c, false) }
func (h *WorkflowHandler) decision(c *gin.Context, approved bool) {
	var in struct {
		Comment string `json:"comment"`
	}
	if c.Request.ContentLength > 0 {
		if e := c.ShouldBindJSON(&in); e != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
			return
		}
	}
	r, e := h.engine.ResumeQueued(c.Request.Context(), c.Param("run_id"), approved, in.Comment, h.maxAttempts)
	if e != nil {
		status := http.StatusConflict
		if errors.Is(e, wf.ErrNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"code": "workflow_decision_failed", "message": e.Error()})
		return
	}
	c.JSON(http.StatusOK, r)
}

func (h *WorkflowHandler) events(c *gin.Context) {
	after, _ := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	events, err := h.engine.Events(c.Request.Context(), c.Param("run_id"), after, limit)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "workflow_events_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": events, "next_after": func() int64 {
		if len(events) == 0 {
			return after
		}
		return events[len(events)-1].Sequence
	}()})
}
func (h *WorkflowHandler) history(c *gin.Context) {
	run, err := h.engine.GetRun(c.Request.Context(), c.Param("run_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "workflow_run_not_found"})
		return
	}
	events, err := h.engine.Events(c.Request.Context(), run.ID, 0, 500)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "workflow_history_failed"})
		return
	}
	nodes, err := h.engine.NodeObservations(c.Request.Context(), run.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "workflow_observations_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"run": run, "events": events, "nodes": nodes})
}
func (h *WorkflowHandler) nodes(c *gin.Context) {
	nodes, err := h.engine.NodeObservations(c.Request.Context(), c.Param("run_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "workflow_nodes_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": nodes})
}
func (h *WorkflowHandler) recoverNode(c *gin.Context) {
	var in struct {
		Action  string `json:"action"`
		Comment string `json:"comment"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	if in.Action != "retry" && in.Action != "skip" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_recovery_action"})
		return
	}
	run, err := h.engine.RecoverNode(c.Request.Context(), c.Param("run_id"), c.Param("node_id"), in.Action, in.Comment, h.maxAttempts)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, wf.ErrNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"code": "workflow_node_recovery_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, run)
}
func (h *WorkflowHandler) eventStream(c *gin.Context) {
	after, _ := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 64)
	if lastID := c.GetHeader("Last-Event-ID"); lastID != "" {
		if parsed, err := strconv.ParseInt(lastID, 10, 64); err == nil && parsed > after {
			after = parsed
		}
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	// Pull-based delivery intentionally has no unbounded per-subscriber channel:
	// a slow client stops reads from SQLite rather than accumulating events in RAM.
	const batchSize = 32
	const writeTimeout = 5 * time.Second
	deadline := time.NewTimer(30 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	controller := http.NewResponseController(c.Writer)
	for {
		if err := controller.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return
		}
		events, err := h.engine.Events(c.Request.Context(), c.Param("run_id"), after, batchSize)
		if err != nil {
			return
		}
		for _, event := range events {
			b, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				return
			}
			if _, writeErr := fmt.Fprintf(c.Writer, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, b); writeErr != nil {
				return
			}
			after = event.Sequence // advance cursor only after the event was accepted by the writer
			if flushErr := controller.Flush(); flushErr != nil {
				return
			}
		}
		if len(events) == batchSize {
			continue
		} // drain backlog in bounded batches
		select {
		case <-c.Request.Context().Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
			if err := controller.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
				return
			}
			if _, err := fmt.Fprint(c.Writer, ": keep-alive\n\n"); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
		}
	}
}
