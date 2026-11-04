package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
	goalservice "github.com/thkx/agentmesh/internal/goal"
	"github.com/thkx/agentmesh/internal/planning"
)

type GoalService interface {
	Create(context.Context, goaldom.Goal) (goaldom.Goal, error)
	Update(context.Context, string, goaldom.Goal) (goaldom.Goal, error)
	Get(context.Context, string) (goaldom.Goal, error)
	List(context.Context, int) ([]goaldom.Goal, error)
	Cancel(context.Context, string) (goaldom.Goal, error)
}

type PlanningService interface {
	Generate(context.Context, string, string) (pland.Plan, error)
	Get(context.Context, string, int) (pland.Plan, error)
	List(context.Context, string, int) ([]pland.Plan, error)
}

type GoalHandler struct {
	goals GoalService
	plans PlanningService
}

func NewGoalHandler(goals GoalService, plans PlanningService) *GoalHandler {
	return &GoalHandler{goals: goals, plans: plans}
}

func (h *GoalHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/goals")
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.PUT("/:id", h.update)
	g.POST("/:id/cancel", h.cancel)
	g.POST("/:id/plans", h.generatePlan)
	g.GET("/:id/plans", h.listPlans)
	g.GET("/:id/plans/:version", h.getPlan)
}

func (h *GoalHandler) create(c *gin.Context) {
	var in goaldom.Goal
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	created, err := h.goals.Create(c.Request.Context(), in)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, goalservice.ErrInvalidRequest) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"code": "goal_create_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (h *GoalHandler) list(c *gin.Context) {
	limit, err := parseLimit(c.Query("limit"), 50, 200)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_limit"})
		return
	}
	items, err := h.goals.List(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "goal_list_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *GoalHandler) update(c *gin.Context) {
	var in goaldom.Goal
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request"})
		return
	}
	updated, err := h.goals.Update(c.Request.Context(), c.Param("id"), in)
	if err != nil {
		if errors.Is(err, goalservice.ErrInvalidRequest) {
			c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_goal", "message": err.Error()})
			return
		}
		writeGoalError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (h *GoalHandler) get(c *gin.Context) {
	g, err := h.goals.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeGoalError(c, err)
		return
	}
	c.JSON(http.StatusOK, g)
}

func (h *GoalHandler) cancel(c *gin.Context) {
	g, err := h.goals.Cancel(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeGoalError(c, err)
		return
	}
	c.JSON(http.StatusOK, g)
}

func (h *GoalHandler) generatePlan(c *gin.Context) {
	var in struct {
		ModelID string `json:"model_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.ModelID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request", "message": "model_id is required"})
		return
	}
	plan, err := h.plans.Generate(c.Request.Context(), c.Param("id"), in.ModelID)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, goaldom.ErrNotFound) {
			status = http.StatusNotFound
		}
		if errors.Is(err, planning.ErrGoalCanceled) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"code": "plan_generation_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, plan)
}

func (h *GoalHandler) listPlans(c *gin.Context) {
	limit, err := parseLimit(c.Query("limit"), 50, 200)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_limit"})
		return
	}
	items, err := h.plans.List(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "plan_list_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *GoalHandler) getPlan(c *gin.Context) {
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_plan_version"})
		return
	}
	plan, err := h.plans.Get(c.Request.Context(), c.Param("id"), version)
	if err != nil {
		if errors.Is(err, goaldom.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"code": "plan_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": "plan_read_failed"})
		return
	}
	c.JSON(http.StatusOK, plan)
}

func writeGoalError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, goaldom.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": "goal_not_found"})
	case errors.Is(err, goaldom.ErrInvalidTransition):
		c.JSON(http.StatusConflict, gin.H{"code": "goal_state_conflict"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"code": "goal_operation_failed", "message": err.Error()})
	}
}

func parseLimit(raw string, fallback, max int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > max {
		return 0, errors.New("limit out of range")
	}
	return n, nil
}
