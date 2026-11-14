package handlers

import (
 "errors"
 "net/http"
 "strconv"
 "time"
 "github.com/gin-gonic/gin"
 "github.com/thkx/agentmesh/internal/collaboration"
)

type CollaborationHandler struct{ service *collaboration.Service }
func NewCollaborationHandler(s *collaboration.Service)*CollaborationHandler{return &CollaborationHandler{service:s}}
func(h *CollaborationHandler)RegisterRoutes(r *gin.RouterGroup){g:=r.Group("/collaboration");g.GET("/workspaces",h.workspaces);g.POST("/workspaces",h.createWorkspace);g.GET("/workspaces/:id",h.getWorkspace);g.GET("/workspaces/:id/agents",h.agents);g.POST("/agents",h.createAgent);g.GET("/agents/:id",h.getAgent);g.GET("/workspaces/:id/channels",h.channels);g.POST("/channels",h.createChannel);g.GET("/channels/:id",h.getChannel);g.GET("/channels/:id/messages",h.messages);g.POST("/channels/:id/messages",h.postMessage);g.POST("/channels/:id/threads",h.thread);g.GET("/workspaces/:id/tasks",h.tasks);g.POST("/tasks",h.createTask);g.GET("/tasks/:id",h.getTask);g.POST("/tasks/:id/assignments",h.assign);g.POST("/tasks/:id/handoff",h.handoff);g.POST("/assignments/:id/claim",h.claim);g.POST("/assignments/:id/renew",h.renew);g.POST("/assignments/:id/complete",h.complete);g.POST("/decisions/evaluate",h.evaluateDecision);g.GET("/workspaces/:id/approvals",h.approvals);g.POST("/approvals/:id/resolve",h.resolveApproval);g.GET("/outbox",h.outbox);g.GET("/inbox/:agent_id",h.listInbox);g.POST("/inbox/:agent_id/events",h.inbox);g.POST("/inbox/:agent_id/:event_id/ack",h.ackInbox)}
func(h *CollaborationHandler)fail(c *gin.Context,e error){var approvalErr *collaboration.ApprovalRequiredError;if errors.As(e,&approvalErr){c.JSON(http.StatusAccepted,gin.H{"outcome":"approval_required","approval_id":approvalErr.ApprovalID});return};code:=400;if errors.Is(e,collaboration.ErrNotFound){code=404}else if errors.Is(e,collaboration.ErrConflict)||errors.Is(e,collaboration.ErrLeaseLost){code=409};c.JSON(code,gin.H{"error":e.Error()})}
func(h *CollaborationHandler)workspaces(c *gin.Context){v,e:=h.service.ListWorkspaces(c);if e!=nil{h.fail(c,e);return};c.JSON(200,gin.H{"items":v})}
func(h *CollaborationHandler)createWorkspace(c *gin.Context){var v collaboration.Workspace;if c.ShouldBindJSON(&v)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};o,e:=h.service.CreateWorkspace(c,v);if e!=nil{h.fail(c,e);return};c.JSON(201,o)}
func(h *CollaborationHandler)getWorkspace(c *gin.Context){v,e:=h.service.GetWorkspace(c,c.Param("id"));if e!=nil{h.fail(c,e);return};c.JSON(200,v)}
func(h *CollaborationHandler)agents(c *gin.Context){v,e:=h.service.ListAgents(c,c.Param("id"));if e!=nil{h.fail(c,e);return};c.JSON(200,gin.H{"items":v})}
func(h *CollaborationHandler)createAgent(c *gin.Context){var v collaboration.AgentIdentity;if c.ShouldBindJSON(&v)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};o,e:=h.service.CreateAgent(c,v);if e!=nil{h.fail(c,e);return};c.JSON(201,o)}
func(h *CollaborationHandler)getAgent(c *gin.Context){v,e:=h.service.GetAgent(c,c.Param("id"));if e!=nil{h.fail(c,e);return};c.JSON(200,v)}
func(h *CollaborationHandler)channels(c *gin.Context){v,e:=h.service.ListChannels(c,c.Param("id"));if e!=nil{h.fail(c,e);return};c.JSON(200,gin.H{"items":v})}
func(h *CollaborationHandler)createChannel(c *gin.Context){var v collaboration.Channel;if c.ShouldBindJSON(&v)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};o,e:=h.service.CreateChannel(c,v);if e!=nil{h.fail(c,e);return};c.JSON(201,o)}
func(h *CollaborationHandler)getChannel(c *gin.Context){v,e:=h.service.GetChannel(c,c.Param("id"));if e!=nil{h.fail(c,e);return};c.JSON(200,v)}
func(h *CollaborationHandler)messages(c *gin.Context){a,_:=strconv.ParseInt(c.DefaultQuery("after","0"),10,64);l,_:=strconv.Atoi(c.DefaultQuery("limit","100"));v,e:=h.service.Messages(c,c.Param("id"),a,l);if e!=nil{h.fail(c,e);return};c.JSON(200,gin.H{"items":v})}
func(h *CollaborationHandler)postMessage(c *gin.Context){var in struct{Role string `json:"role"`;Name string `json:"name"`;Content string `json:"content"`};if c.ShouldBindJSON(&in)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};if in.Role==""{in.Role="user"};v,e:=h.service.PostMessage(c,c.Param("id"),in.Role,in.Name,in.Content);if e!=nil{h.fail(c,e);return};c.JSON(201,v)}
func(h *CollaborationHandler)thread(c *gin.Context){var v collaboration.Thread;if c.ShouldBindJSON(&v)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};v.ChannelID=c.Param("id");v,e:=h.service.CreateThread(c,v);if e!=nil{h.fail(c,e);return};c.JSON(201,v)}
func(h *CollaborationHandler)tasks(c *gin.Context){v,e:=h.service.ListTasks(c,c.Param("id"));if e!=nil{h.fail(c,e);return};c.JSON(200,gin.H{"items":v})}
func(h *CollaborationHandler)createTask(c *gin.Context){var v collaboration.Task;if c.ShouldBindJSON(&v)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};o,e:=h.service.CreateTask(c,v);if e!=nil{h.fail(c,e);return};c.JSON(201,o)}
func(h *CollaborationHandler)getTask(c *gin.Context){v,e:=h.service.GetTask(c,c.Param("id"));if e!=nil{h.fail(c,e);return};c.JSON(200,v)}
func(h *CollaborationHandler)assign(c *gin.Context){var in struct{AgentID string `json:"agent_id"`;AssignedBy string `json:"assigned_by"`};if c.ShouldBindJSON(&in)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};v,e:=h.service.AssignTask(c,c.Param("id"),in.AgentID,in.AssignedBy);if e!=nil{h.fail(c,e);return};c.JSON(201,v)}
func(h *CollaborationHandler)claim(c *gin.Context){var in struct{Owner string `json:"owner"`;LeaseSeconds int `json:"lease_seconds"`};if c.ShouldBindJSON(&in)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};if in.LeaseSeconds==0{in.LeaseSeconds=300};v,e:=h.service.ClaimAssignment(c,c.Param("id"),in.Owner,time.Duration(in.LeaseSeconds)*time.Second);if e!=nil{h.fail(c,e);return};c.JSON(200,v)}
func(h *CollaborationHandler)complete(c *gin.Context){var in struct{Owner string `json:"owner"`;ClaimVersion int64 `json:"claim_version"`};if c.ShouldBindJSON(&in)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};if e:=h.service.CompleteAssignment(c,c.Param("id"),in.Owner,in.ClaimVersion);e!=nil{h.fail(c,e);return};c.Status(http.StatusNoContent)}
func(h *CollaborationHandler)outbox(c *gin.Context){v,e:=h.service.PendingOutbox(c,100);if e!=nil{h.fail(c,e);return};c.JSON(200,gin.H{"items":v})}
func(h *CollaborationHandler)inbox(c *gin.Context){var e collaboration.Event;if c.ShouldBindJSON(&e)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};ok,err:=h.service.ReceiveEvent(c,c.Param("agent_id"),e);if err!=nil{h.fail(c,err);return};c.JSON(202,gin.H{"accepted":ok})}

func(h *CollaborationHandler)listInbox(c *gin.Context){limit,_:=strconv.Atoi(c.DefaultQuery("limit","100"));v,e:=h.service.ListInbox(c,c.Param("agent_id"),c.DefaultQuery("status","pending"),limit);if e!=nil{h.fail(c,e);return};c.JSON(200,gin.H{"items":v})}
func(h *CollaborationHandler)ackInbox(c *gin.Context){var in struct{Status string `json:"status"`};if c.ShouldBindJSON(&in)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};if e:=h.service.AckInbox(c,c.Param("agent_id"),c.Param("event_id"),in.Status);e!=nil{h.fail(c,e);return};c.Status(http.StatusNoContent)}

func(h *CollaborationHandler)handoff(c *gin.Context){var in collaboration.Handoff;if c.ShouldBindJSON(&in)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};task,assignment,e:=h.service.HandoffTask(c,c.Param("id"),in,"");if e!=nil{h.fail(c,e);return};c.JSON(201,gin.H{"task":task,"assignment":assignment})}
func(h *CollaborationHandler)renew(c *gin.Context){var in struct{Owner string `json:"owner"`;ClaimVersion int64 `json:"claim_version"`;LeaseSeconds int `json:"lease_seconds"`};if c.ShouldBindJSON(&in)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};if in.LeaseSeconds==0{in.LeaseSeconds=300};v,e:=h.service.RenewAssignment(c,c.Param("id"),in.Owner,in.ClaimVersion,time.Duration(in.LeaseSeconds)*time.Second);if e!=nil{h.fail(c,e);return};c.JSON(200,v)}

func(h *CollaborationHandler)evaluateDecision(c *gin.Context){var in collaboration.DecisionRequest;if c.ShouldBindJSON(&in)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};v,e:=h.service.EvaluateDecision(c,in);if e!=nil{h.fail(c,e);return};c.JSON(200,v)}
func(h *CollaborationHandler)approvals(c *gin.Context){v,e:=h.service.ListApprovals(c,c.Param("id"),c.DefaultQuery("status","pending"),100);if e!=nil{h.fail(c,e);return};c.JSON(200,gin.H{"items":v})}
func(h *CollaborationHandler)resolveApproval(c *gin.Context){var in struct{ReviewerID string `json:"reviewer_id"`;Status string `json:"status"`;Note string `json:"note"`};if c.ShouldBindJSON(&in)!=nil{c.JSON(400,gin.H{"error":"invalid_request"});return};v,e:=h.service.ResolveApproval(c,c.Param("id"),in.ReviewerID,in.Status,in.Note);if e!=nil{h.fail(c,e);return};c.JSON(200,v)}
