package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/agent/contextmanager"
	"net/http"
)

type ConversationContextHandler struct{ assembler *contextmanager.Assembler }

func NewConversationContextHandler(a *contextmanager.Assembler) *ConversationContextHandler {
	return &ConversationContextHandler{assembler: a}
}
func (h *ConversationContextHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/conversations/:id/context", h.build)
}
func (h *ConversationContextHandler) build(c *gin.Context) {
	var in struct {
		Query          string   `json:"query"`
		GoalID         string   `json:"goal_id"`
		AgentIDs       []string `json:"agent_ids"`
		ModelWindow    int      `json:"model_window"`
		OutputReserve  int      `json:"output_reserve"`
		SystemReserve  int      `json:"system_reserve"`
		SafetyMargin   int      `json:"safety_margin"`
		Compress       bool     `json:"compress"`
		SummaryModelID string   `json:"summary_model_id"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	result, err := h.assembler.Build(c.Request.Context(), contextmanager.ContextRequest{ConversationID: c.Param("id"), Query: in.Query, GoalID: in.GoalID, AgentIDs: in.AgentIDs, Budget: contextmanager.Budget{ModelWindow: in.ModelWindow, OutputReserve: in.OutputReserve, SystemReserve: in.SystemReserve, SafetyMargin: in.SafetyMargin}, Compress: in.Compress, SummaryModelID: in.SummaryModelID})
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, result)
}
