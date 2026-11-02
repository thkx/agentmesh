package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
)

type goalHandlerGoalService struct{ goal goaldom.Goal }

func (s *goalHandlerGoalService) Create(_ context.Context, g goaldom.Goal) (goaldom.Goal, error) {
	g.ID = "g1"
	g.Status = goaldom.StatusActive
	s.goal = g
	return g, nil
}
func (s *goalHandlerGoalService) Update(_ context.Context, _ string, g goaldom.Goal) (goaldom.Goal, error) {
	s.goal = g
	return g, nil
}
func (s *goalHandlerGoalService) Get(context.Context, string) (goaldom.Goal, error) {
	return s.goal, nil
}
func (s *goalHandlerGoalService) List(context.Context, int) ([]goaldom.Goal, error) {
	return []goaldom.Goal{s.goal}, nil
}
func (s *goalHandlerGoalService) Cancel(context.Context, string) (goaldom.Goal, error) {
	s.goal.Status = goaldom.StatusCanceled
	return s.goal, nil
}

type goalHandlerPlanningService struct{ plan pland.Plan }

func (s *goalHandlerPlanningService) Generate(_ context.Context, goalID, modelID string) (pland.Plan, error) {
	s.plan = pland.Plan{ID: "p1", GoalID: goalID, Version: 1, ModelID: modelID}
	return s.plan, nil
}
func (s *goalHandlerPlanningService) Get(context.Context, string, int) (pland.Plan, error) {
	return s.plan, nil
}
func (s *goalHandlerPlanningService) List(context.Context, string, int) ([]pland.Plan, error) {
	return []pland.Plan{s.plan}, nil
}

func TestGoalHandlerCreateAndGeneratePlan(t *testing.T) {
	gin.SetMode(gin.TestMode)
	goals := &goalHandlerGoalService{}
	plans := &goalHandlerPlanningService{}
	router := gin.New()
	api := router.Group("/api/v1")
	NewGoalHandler(goals, plans).RegisterRoutes(api)

	create := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/goals", strings.NewReader(`{"description":"Research a product","constraints":["solo developer"]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(create, request)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}

	generate := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/goals/g1/plans", strings.NewReader(`{"model_id":"model-1"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(generate, request)
	if generate.Code != http.StatusCreated {
		t.Fatalf("plan status=%d body=%s", generate.Code, generate.Body.String())
	}
	if plans.plan.GoalID != "g1" || plans.plan.ModelID != "model-1" {
		t.Fatalf("unexpected plan: %+v", plans.plan)
	}
}
