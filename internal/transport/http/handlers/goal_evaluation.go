package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	evaldom "github.com/thkx/agentmesh/internal/domain/goalevaluation"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/goalevaluation"
)

type GoalEvaluationService interface {
	Evaluate(context.Context, string, goalevaluation.EvaluateRequest) (evaldom.Evaluation, error)
	Get(context.Context, string, string) (evaldom.Evaluation, error)
	List(context.Context, string, int) ([]evaldom.Evaluation, error)
}

type GoalEvaluationHandler struct{ service GoalEvaluationService }

func NewGoalEvaluationHandler(service GoalEvaluationService) *GoalEvaluationHandler {
	return &GoalEvaluationHandler{service: service}
}
func (h *GoalEvaluationHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/goals/:id/evaluations")
	g.POST("", h.evaluate)
	g.GET("", h.list)
	g.GET("/:run_id", h.get)
}
func (h *GoalEvaluationHandler) evaluate(c *gin.Context) {
	var req goalevaluation.EvaluateRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.RunID == "" || req.ModelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request", "message": "run_id and model_id are required"})
		return
	}
	item, err := h.service.Evaluate(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, goalevaluation.ErrExecutionNotFound) || errors.Is(err, goalevaluation.ErrNotFound) || errors.Is(err, goaldom.ErrNotFound) || errors.Is(err, wf.ErrNotFound) {
			status = http.StatusNotFound
		}
		if errors.Is(err, goalevaluation.ErrGoalNotActive) || errors.Is(err, goalevaluation.ErrGoalNotEvaluable) || errors.Is(err, goalevaluation.ErrRunNotTerminal) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"code": "goal_evaluation_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, item)
}
func (h *GoalEvaluationHandler) list(c *gin.Context) {
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_limit"})
			return
		}
		limit = n
	}
	items, err := h.service.List(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "goal_evaluation_list_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
func (h *GoalEvaluationHandler) get(c *gin.Context) {
	item, err := h.service.Get(c.Request.Context(), c.Param("id"), c.Param("run_id"))
	if err != nil {
		if errors.Is(err, goalevaluation.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"code": "goal_evaluation_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": "goal_evaluation_read_failed"})
		return
	}
	c.JSON(http.StatusOK, item)
}
