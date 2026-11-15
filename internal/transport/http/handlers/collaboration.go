package handlers

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/agentcatalog"
	"github.com/thkx/agentmesh/internal/collaboration"
	"github.com/thkx/agentmesh/internal/workspace"
	"net/http"
	"strconv"
	"time"
)

type CollaborationHandler struct {
	service          *collaboration.Service
	definitions      *agentcatalog.Service
	workspaceManager *workspace.Manager
}

func (h *CollaborationHandler) SetRuntimeBindingDependencies(defs *agentcatalog.Service, workspaces *workspace.Manager) {
	h.definitions = defs
	h.workspaceManager = workspaces
}
func NewCollaborationHandler(s *collaboration.Service) *CollaborationHandler {
	return &CollaborationHandler{service: s}
}
func (h *CollaborationHandler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/collaboration")
	g.GET("/workspaces", h.workspaces)
	g.POST("/workspaces", h.createWorkspace)
	g.GET("/workspaces/:id", h.getWorkspace)
	g.GET("/workspaces/:id/agents", h.agents)
	g.POST("/agents", h.createAgent)
	g.GET("/agents/:id", h.getAgent)
	g.POST("/agents/:id/runtime-binding", h.bindRuntime)
	g.GET("/agents/:id/runtime-binding", h.getRuntimeBinding)
	g.GET("/agents/:id/session", h.session)
	g.GET("/agents/:id/session/transcript", h.sessionTranscript)
	g.POST("/artifacts", h.createArtifact)
	g.GET("/workspaces/:id/artifacts", h.listArtifacts)
	g.GET("/artifacts/:id", h.getArtifact)
	g.GET("/artifacts/:id/versions/:version", h.getArtifactVersion)
	g.POST("/artifacts/:id/versions", h.addArtifactVersion)
	g.GET("/artifacts/:id/evidence", h.listArtifactEvidence)
	g.POST("/artifacts/:id/evidence", h.addArtifactEvidence)
	g.POST("/artifacts/:id/verify", h.verifyArtifact)
	g.GET("/artifacts/:id/verifications", h.listArtifactVerifications)
	g.GET("/artifacts/:id/reviews", h.listArtifactReviews)
	g.POST("/artifacts/:id/submit", h.submitArtifact)
	g.POST("/artifacts/:id/review", h.reviewArtifact)
	g.GET("/agents/:id/task-results", h.agentTaskResults)
	g.GET("/agents/:id/task-results/:assignment_id", h.agentTaskResult)
	g.POST("/agents/:id/task-results/:assignment_id/review", h.reviewAgentTaskResult)
	g.GET("/workspaces/:id/channels", h.channels)
	g.POST("/channels", h.createChannel)
	g.GET("/channels/:id", h.getChannel)
	g.GET("/channels/:id/messages", h.messages)
	g.POST("/channels/:id/messages", h.postMessage)
	g.POST("/channels/:id/threads", h.thread)
	g.GET("/workspaces/:id/tasks", h.tasks)
	g.POST("/tasks", h.createTask)
	g.GET("/tasks/:id", h.getTask)
	g.GET("/tasks/:id/progress", h.taskProgress)
	g.POST("/tasks/:id/assignments", h.assign)
	g.POST("/tasks/:id/handoff", h.handoff)
	g.POST("/assignments/:id/claim", h.claim)
	g.POST("/assignments/:id/renew", h.renew)
	g.POST("/assignments/:id/complete", h.complete)
	g.POST("/assignments/:id/fail", h.failAssignment)
	g.POST("/assignments/:id/retry", h.retryAssignment)
	g.POST("/decisions/evaluate", h.evaluateDecision)
	g.GET("/agents/:id/decision-audit", h.decisionAudit)
	g.GET("/workspaces/:id/approvals", h.approvals)
	g.POST("/approvals/:id/resolve", h.resolveApproval)
	g.GET("/outbox", h.outbox)
	g.GET("/outbox/dead-letters", h.deadLetters)
	g.POST("/outbox/dead-letters/:id/requeue", h.requeueDeadLetter)
	g.GET("/inbox/:agent_id", h.listInbox)
	g.POST("/inbox/:agent_id/events", h.inbox)
	g.POST("/inbox/:agent_id/claim", h.claimInbox)
	g.POST("/inbox/:agent_id/:event_id/renew", h.renewInbox)
	g.POST("/inbox/:agent_id/:event_id/ack-lease", h.ackInboxLease)
	g.POST("/inbox/:agent_id/:event_id/fail", h.failInboxLease)
	g.POST("/inbox/:agent_id/:event_id/requeue", h.requeueInbox)
	g.POST("/inbox/:agent_id/:event_id/ack", h.ackInbox)
}
func (h *CollaborationHandler) fail(c *gin.Context, e error) {
	var approvalErr *collaboration.ApprovalRequiredError
	if errors.As(e, &approvalErr) {
		c.JSON(http.StatusAccepted, gin.H{"outcome": "approval_required", "approval_id": approvalErr.ApprovalID})
		return
	}
	code := 400
	if errors.Is(e, collaboration.ErrNotFound) {
		code = 404
	} else if errors.Is(e, collaboration.ErrConflict) || errors.Is(e, collaboration.ErrLeaseLost) || errors.Is(e, collaboration.ErrInboxLeaseLost) || errors.Is(e, collaboration.ErrWorkspaceConcurrencyLimit) || errors.Is(e, collaboration.ErrAgentBusy) || errors.Is(e, collaboration.ErrCapabilityMismatch) {
		code = 409
	}
	c.JSON(code, gin.H{"error": e.Error()})
}
func (h *CollaborationHandler) workspaces(c *gin.Context) {
	v, e := h.service.ListWorkspaces(c)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, gin.H{"items": v})
}
func (h *CollaborationHandler) createWorkspace(c *gin.Context) {
	var v collaboration.Workspace
	if c.ShouldBindJSON(&v) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	o, e := h.service.CreateWorkspace(c, v)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(201, o)
}
func (h *CollaborationHandler) getWorkspace(c *gin.Context) {
	v, e := h.service.GetWorkspace(c, c.Param("id"))
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, v)
}
func (h *CollaborationHandler) agents(c *gin.Context) {
	v, e := h.service.ListAgents(c, c.Param("id"))
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, gin.H{"items": v})
}
func (h *CollaborationHandler) createAgent(c *gin.Context) {
	var v collaboration.AgentIdentity
	if c.ShouldBindJSON(&v) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	o, e := h.service.CreateAgent(c, v)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(201, o)
}
func (h *CollaborationHandler) getAgent(c *gin.Context) {
	v, e := h.service.GetAgent(c, c.Param("id"))
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, v)
}
func (h *CollaborationHandler) channels(c *gin.Context) {
	v, e := h.service.ListChannels(c, c.Param("id"))
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, gin.H{"items": v})
}
func (h *CollaborationHandler) createChannel(c *gin.Context) {
	var v collaboration.Channel
	if c.ShouldBindJSON(&v) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	o, e := h.service.CreateChannel(c, v)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(201, o)
}
func (h *CollaborationHandler) getChannel(c *gin.Context) {
	v, e := h.service.GetChannel(c, c.Param("id"))
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, v)
}
func (h *CollaborationHandler) messages(c *gin.Context) {
	a, _ := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 64)
	l, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	v, e := h.service.Messages(c, c.Param("id"), a, l)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, gin.H{"items": v})
}
func (h *CollaborationHandler) postMessage(c *gin.Context) {
	var in struct {
		Role    string `json:"role"`
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	if in.Role == "" {
		in.Role = "user"
	}
	v, e := h.service.PostMessage(c, c.Param("id"), in.Role, in.Name, in.Content)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(201, v)
}
func (h *CollaborationHandler) thread(c *gin.Context) {
	var v collaboration.Thread
	if c.ShouldBindJSON(&v) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	v.ChannelID = c.Param("id")
	v, e := h.service.CreateThread(c, v)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(201, v)
}
func (h *CollaborationHandler) tasks(c *gin.Context) {
	v, e := h.service.ListTasks(c, c.Param("id"))
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, gin.H{"items": v})
}
func (h *CollaborationHandler) createTask(c *gin.Context) {
	var v collaboration.Task
	if c.ShouldBindJSON(&v) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	o, e := h.service.CreateTask(c, v)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(201, o)
}
func (h *CollaborationHandler) getTask(c *gin.Context) {
	v, e := h.service.GetTask(c, c.Param("id"))
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, v)
}
func (h *CollaborationHandler) assign(c *gin.Context) {
	var in struct {
		AgentID    string `json:"agent_id"`
		AssignedBy string `json:"assigned_by"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	v, e := h.service.AssignTask(c, c.Param("id"), in.AgentID, in.AssignedBy)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(201, v)
}
func (h *CollaborationHandler) claim(c *gin.Context) {
	var in struct {
		Owner        string `json:"owner"`
		LeaseSeconds int    `json:"lease_seconds"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	if in.LeaseSeconds == 0 {
		in.LeaseSeconds = 300
	}
	v, e := h.service.ClaimAssignment(c, c.Param("id"), in.Owner, time.Duration(in.LeaseSeconds)*time.Second)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, v)
}
func (h *CollaborationHandler) complete(c *gin.Context) {
	var in struct {
		Owner        string `json:"owner"`
		ClaimVersion int64  `json:"claim_version"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	if e := h.service.CompleteAssignment(c, c.Param("id"), in.Owner, in.ClaimVersion); e != nil {
		h.fail(c, e)
		return
	}
	c.Status(http.StatusNoContent)
}
func (h *CollaborationHandler) outbox(c *gin.Context) {
	v, e := h.service.PendingOutbox(c, 100)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, gin.H{"items": v})
}
func (h *CollaborationHandler) deadLetters(c *gin.Context) {
	v, e := h.service.ListDeadLetters(c, 100)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, gin.H{"items": v})
}
func (h *CollaborationHandler) requeueDeadLetter(c *gin.Context) {
	if e := h.service.RequeueDeadLetter(c, c.Param("id")); e != nil {
		h.fail(c, e)
		return
	}
	c.Status(http.StatusNoContent)
}
func (h *CollaborationHandler) inbox(c *gin.Context) {
	var e collaboration.Event
	if c.ShouldBindJSON(&e) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	ok, err := h.service.ReceiveEvent(c, c.Param("agent_id"), e)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(202, gin.H{"accepted": ok})
}

func (h *CollaborationHandler) listInbox(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	v, e := h.service.ListInbox(c, c.Param("agent_id"), c.DefaultQuery("status", "pending"), limit)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, gin.H{"items": v})
}
func (h *CollaborationHandler) ackInbox(c *gin.Context) {
	var in struct {
		Status string `json:"status"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	if e := h.service.AckInbox(c, c.Param("agent_id"), c.Param("event_id"), in.Status); e != nil {
		h.fail(c, e)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *CollaborationHandler) handoff(c *gin.Context) {
	var in collaboration.Handoff
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	task, assignment, e := h.service.HandoffTask(c, c.Param("id"), in, in.RequesterAgentID)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(201, gin.H{"task": task, "assignment": assignment})
}
func (h *CollaborationHandler) renew(c *gin.Context) {
	var in struct {
		Owner        string `json:"owner"`
		ClaimVersion int64  `json:"claim_version"`
		LeaseSeconds int    `json:"lease_seconds"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	if in.LeaseSeconds == 0 {
		in.LeaseSeconds = 300
	}
	v, e := h.service.RenewAssignment(c, c.Param("id"), in.Owner, in.ClaimVersion, time.Duration(in.LeaseSeconds)*time.Second)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, v)
}

func (h *CollaborationHandler) decisionAudit(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	items, err := h.service.ListDecisionAudit(c, c.Param("id"), limit)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *CollaborationHandler) evaluateDecision(c *gin.Context) {
	var in collaboration.DecisionRequest
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	v, e := h.service.EvaluateDecision(c, in)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, v)
}
func (h *CollaborationHandler) approvals(c *gin.Context) {
	v, e := h.service.ListApprovals(c, c.Param("id"), c.DefaultQuery("status", "pending"), 100)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, gin.H{"items": v})
}
func (h *CollaborationHandler) resolveApproval(c *gin.Context) {
	var in struct {
		ReviewerID string `json:"reviewer_id"`
		Status     string `json:"status"`
		Note       string `json:"note"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	v, e := h.service.ResolveApproval(c, c.Param("id"), in.ReviewerID, in.Status, in.Note)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, v)
}

func (h *CollaborationHandler) claimInbox(c *gin.Context) {
	var in struct {
		Owner        string `json:"owner"`
		LeaseSeconds int    `json:"lease_seconds"`
		MaxAttempts  int    `json:"max_attempts"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	if in.LeaseSeconds == 0 {
		in.LeaseSeconds = 300
	}
	v, e := h.service.ClaimInbox(c, c.Param("agent_id"), collaboration.InboxLeaseOptions{Owner: in.Owner, Duration: time.Duration(in.LeaseSeconds) * time.Second, MaxAttempts: in.MaxAttempts})
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, v)
}
func (h *CollaborationHandler) renewInbox(c *gin.Context) {
	var in struct {
		Owner        string `json:"owner"`
		Token        string `json:"lease_token"`
		LeaseSeconds int    `json:"lease_seconds"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	if in.LeaseSeconds == 0 {
		in.LeaseSeconds = 300
	}
	expires, e := h.service.RenewInboxLease(c, c.Param("agent_id"), c.Param("event_id"), in.Owner, in.Token, time.Duration(in.LeaseSeconds)*time.Second)
	if e != nil {
		h.fail(c, e)
		return
	}
	c.JSON(200, gin.H{"lease_expires_at": expires})
}
func (h *CollaborationHandler) ackInboxLease(c *gin.Context) {
	var in struct {
		Owner  string `json:"owner"`
		Token  string `json:"lease_token"`
		Status string `json:"status"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	if e := h.service.AckInboxLease(c, c.Param("agent_id"), c.Param("event_id"), in.Owner, in.Token, in.Status); e != nil {
		h.fail(c, e)
		return
	}
	c.Status(http.StatusNoContent)
}
func (h *CollaborationHandler) requeueInbox(c *gin.Context) {
	if err := h.service.RequeueInbox(c, c.Param("agent_id"), c.Param("event_id")); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func (h *CollaborationHandler) failInboxLease(c *gin.Context) {
	var in struct {
		Owner       string `json:"owner"`
		Token       string `json:"lease_token"`
		Reason      string `json:"reason"`
		MaxAttempts int    `json:"max_attempts"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	if in.MaxAttempts == 0 {
		in.MaxAttempts = 8
	}
	next, e := h.service.FailInboxLease(c, c.Param("agent_id"), c.Param("event_id"), in.Owner, in.Token, in.Reason, in.MaxAttempts)
	if e != nil {
		h.fail(c, e)
		return
	}
	if next.IsZero() {
		c.JSON(200, gin.H{"terminal": true})
		return
	}
	c.JSON(200, gin.H{"retry_at": next})
}

func (h *CollaborationHandler) bindRuntime(c *gin.Context) {
	if h.definitions == nil || h.workspaceManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "runtime binding is unavailable"})
		return
	}
	var in struct {
		DefinitionID       string `json:"definition_id"`
		RuntimeWorkspaceID string `json:"runtime_workspace_id"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	def, err := h.definitions.Get(c, in.DefinitionID)
	if err != nil {
		h.fail(c, err)
		return
	}
	if _, err = h.workspaceManager.Resolve(in.RuntimeWorkspaceID); err != nil {
		c.JSON(400, gin.H{"error": "runtime_workspace_not_found"})
		return
	}
	v, err := h.service.BindRuntime(c, c.Param("id"), in.RuntimeWorkspaceID, def)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(200, v)
}
func (h *CollaborationHandler) getRuntimeBinding(c *gin.Context) {
	v, err := h.service.GetRuntimeBinding(c, c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(200, v)
}
func (h *CollaborationHandler) session(c *gin.Context) {
	v, err := h.service.GetActiveAgentSession(c, c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(200, v)
}

func (h *CollaborationHandler) sessionTranscript(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := h.service.ListAgentSessionTranscript(c, c.Param("id"), limit)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *CollaborationHandler) sessionTranscript(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := h.service.ListAgentSessionTranscript(c, c.Param("id"), limit)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *CollaborationHandler) agentTaskResults(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	results, err := h.service.ListTaskResults(c, c.Param("id"), limit)
	if err != nil {
		h.fail(c, err)
		return
	}
	assessments := make([]collaboration.TaskResultAssessment, 0, len(results))
	for _, result := range results {
		assessment, readErr := h.service.GetTaskResultAssessment(c, c.Param("id"), result.AssignmentID)
		if readErr == nil {
			assessments = append(assessments, assessment)
		} else if !errors.Is(readErr, collaboration.ErrNotFound) {
			h.fail(c, readErr)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": results, "assessments": assessments})
}
func (h *CollaborationHandler) agentTaskResult(c *gin.Context) {
	result, err := h.service.GetTaskResult(c, c.Param("id"), c.Param("assignment_id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	assessment, err := h.service.GetTaskResultAssessment(c, c.Param("id"), c.Param("assignment_id"))
	if err != nil && !errors.Is(err, collaboration.ErrNotFound) {
		h.fail(c, err)
		return
	}
	audit, err := h.service.ListTaskResultReviewAudit(c, c.Param("id"), c.Param("assignment_id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": result, "assessment": assessment, "review_audit": audit})
}
func (h *CollaborationHandler) reviewAgentTaskResult(c *gin.Context) {
	var in struct {
		ReviewerID string `json:"reviewer_id"`
		Outcome    string `json:"outcome"`
		Note       string `json:"note"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	assessment, err := h.service.ResolveTaskResultAssessment(c, c.Param("id"), c.Param("assignment_id"), in.ReviewerID, in.Outcome, in.Note)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, assessment)
}
func (h *CollaborationHandler) taskProgress(c *gin.Context) {
	progress, err := h.service.GetTaskChildProgress(c, c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, progress)
}
func (h *CollaborationHandler) failAssignment(c *gin.Context) {
	var in struct {
		Owner     string `json:"owner"`
		Version   int64  `json:"version"`
		Reason    string `json:"reason"`
		Retryable bool   `json:"retryable"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if err := h.service.FailAssignmentWithRetry(c, c.Param("id"), in.Owner, in.Version, in.Reason, in.Retryable); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "failed", "retryable": in.Retryable})
}
func (h *CollaborationHandler) retryAssignment(c *gin.Context) {
	var in struct {
		RequestedBy string `json:"requested_by"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	assignment, err := h.service.RetryAssignment(c, c.Param("id"), in.RequestedBy)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, assignment)
}

func (h *CollaborationHandler) createArtifact(c *gin.Context) {
	var in collaboration.CreateArtifactRequest
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	artifact, version, err := h.service.CreateArtifact(c, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"artifact": artifact, "version": version})
}

func (h *CollaborationHandler) getArtifact(c *gin.Context) {
	artifact, err := h.service.GetArtifact(c, c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, artifact)
}

func (h *CollaborationHandler) getArtifactVersion(c *gin.Context) {
	versionNumber, err := strconv.Atoi(c.Param("version"))
	if err != nil || versionNumber < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_version"})
		return
	}
	version, err := h.service.GetArtifactVersion(c, c.Param("id"), versionNumber)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, version)
}

func (h *CollaborationHandler) addArtifactVersion(c *gin.Context) {
	var in collaboration.CreateArtifactRequest
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	version, err := h.service.AddArtifactVersion(c, c.Param("id"), in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, version)
}

func (h *CollaborationHandler) listArtifactEvidence(c *gin.Context) {
	items, err := h.service.ListArtifactEvidence(c, c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *CollaborationHandler) addArtifactEvidence(c *gin.Context) {
	var in collaboration.AddArtifactEvidenceRequest
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	item, err := h.service.AddArtifactEvidence(c, c.Param("id"), in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (h *CollaborationHandler) verifyArtifact(c *gin.Context) {
	var in struct {
		Version int                                      `json:"version"`
		Policy  collaboration.ArtifactVerificationPolicy `json:"policy"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	result, err := h.service.VerifyArtifact(c, c.Param("id"), in.Version, in.Policy)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *CollaborationHandler) submitArtifact(c *gin.Context) {
	artifact, err := h.service.SubmitArtifact(c, c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, artifact)
}

func (h *CollaborationHandler) reviewArtifact(c *gin.Context) {
	var in struct {
		ReviewerID string `json:"reviewer_id"`
		Outcome    string `json:"outcome"`
		Note       string `json:"note"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	review, err := h.service.ReviewArtifact(c, c.Param("id"), in.ReviewerID, in.Outcome, in.Note)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, review)
}

func (h *CollaborationHandler) listArtifacts(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := h.service.ListArtifacts(c, c.Param("id"), limit)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *CollaborationHandler) listArtifactReviews(c *gin.Context) {
	items, err := h.service.ListArtifactReviews(c, c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *CollaborationHandler) listArtifactVerifications(c *gin.Context) {
	items, err := h.service.ListArtifactVerifications(c, c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
