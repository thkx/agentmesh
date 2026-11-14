package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
	"github.com/thkx/agentmesh/internal/execution/manager"
)

type ManagedExecutionHandler struct{ manager *manager.Manager }

func NewManagedExecutionHandler(m *manager.Manager)*ManagedExecutionHandler{return &ManagedExecutionHandler{manager:m}}

func(h *ManagedExecutionHandler)RegisterRoutes(r *gin.RouterGroup){
	g:=r.Group("/managed-executions")
	g.POST("",h.submit)
	g.GET("",h.list)
	g.GET("/:id",h.get)
	g.DELETE("/:id",h.cancel)
}

func(h *ManagedExecutionHandler)submit(c *gin.Context){
	var in struct{
		Kind dom.Kind `json:"kind"`
		Runtime dom.Runtime `json:"runtime"`
		TaskID string `json:"task_id"`
		Input []byte `json:"input"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err:=c.ShouldBindJSON(&in);err!=nil{c.JSON(http.StatusBadRequest,gin.H{"code":"invalid_request","message":err.Error()});return}
	if key:=c.GetHeader("Idempotency-Key");key!=""{in.IdempotencyKey=key}
	e,err:=h.manager.Submit(c.Request.Context(),manager.SubmitRequest{Kind:in.Kind,Runtime:in.Runtime,TaskID:in.TaskID,Input:in.Input,IdempotencyKey:in.IdempotencyKey})
	if err!=nil{
		status:=http.StatusBadRequest
		if errors.Is(err,dom.ErrConflict){status=http.StatusConflict}
		c.JSON(status,gin.H{"code":"execution_submit_failed","message":err.Error(),"execution":e});return
	}
	c.JSON(http.StatusAccepted,e)
}

func(h *ManagedExecutionHandler)list(c *gin.Context){
	limit,_:=strconv.Atoi(c.DefaultQuery("limit","50"));offset,_:=strconv.Atoi(c.DefaultQuery("offset","0"))
	items,err:=h.manager.List(c.Request.Context(),dom.ListFilter{Status:dom.LifecycleStatus(c.Query("status")),Kind:dom.Kind(c.Query("kind")),Runtime:dom.Runtime(c.Query("runtime")),TaskID:c.Query("task_id"),Limit:limit,Offset:offset})
	if err!=nil{c.JSON(http.StatusInternalServerError,gin.H{"code":"execution_list_failed"});return}
	c.JSON(http.StatusOK,gin.H{"items":items,"limit":limit,"offset":offset})
}

func(h *ManagedExecutionHandler)get(c *gin.Context){
	e,err:=h.manager.Get(c.Request.Context(),c.Param("id"))
	if err!=nil{if errors.Is(err,dom.ErrNotFound){c.JSON(http.StatusNotFound,gin.H{"code":"execution_not_found"});return};c.JSON(http.StatusConflict,gin.H{"code":"execution_observation_failed","message":err.Error()});return}
	c.JSON(http.StatusOK,e)
}

func(h *ManagedExecutionHandler)cancel(c *gin.Context){
	if err:=h.manager.Cancel(c.Request.Context(),c.Param("id"));err!=nil{
		status:=http.StatusConflict;if errors.Is(err,dom.ErrNotFound){status=http.StatusNotFound}
		c.JSON(status,gin.H{"code":"execution_cancel_failed","message":err.Error()});return
	}
	c.Status(http.StatusAccepted)
}
