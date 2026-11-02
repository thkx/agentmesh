package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	"github.com/thkx/agentmesh/internal/planworkflow"
)

type PlanExecutionService interface {
	Start(context.Context, string, int, planworkflow.StartRequest) (planworkflow.Execution, error)
}

type PlanExecutionHandler struct{ service PlanExecutionService }

func NewPlanExecutionHandler(service PlanExecutionService) *PlanExecutionHandler {
	return &PlanExecutionHandler{service: service}
}

func (h *PlanExecutionHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/goals/:id/plans/:version/runs", h.start)
}

func (h *PlanExecutionHandler) start(c *gin.Context) {
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_plan_version"})
		return
	}
	var req planworkflow.StartRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request", "message": err.Error()})
		return
	}
	result, err := h.service.Start(c.Request.Context(), c.Param("id"), version, req)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, goaldom.ErrNotFound) {
			status = http.StatusNotFound
		}
		if errors.Is(err, planworkflow.ErrIdempotencyConflict) || errors.Is(err, planworkflow.ErrGoalInactive) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"code": "plan_execution_start_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, result)
}
