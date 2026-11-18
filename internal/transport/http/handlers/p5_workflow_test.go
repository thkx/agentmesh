package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/workflow/yamlcodec"
)

type p5WorkflowEngineStub struct {
	definitions map[string]wf.Definition
}

func (s *p5WorkflowEngineStub) PutDefinition(_ context.Context, d wf.Definition) error {
	if s.definitions == nil {
		s.definitions = make(map[string]wf.Definition)
	}
	s.definitions[d.ID] = d
	return nil
}
func (s *p5WorkflowEngineStub) Definition(_ context.Context, id string, _ int) (wf.Definition, error) {
	d, ok := s.definitions[id]
	if !ok {
		return wf.Definition{}, wf.ErrNotFound
	}
	return d, nil
}
func (*p5WorkflowEngineStub) Start(context.Context, string, string, map[string]any) (wf.Run, error) {
	return wf.Run{}, errors.New("not implemented")
}
func (*p5WorkflowEngineStub) StartQueued(context.Context, string, string, map[string]any, int) (wf.Run, error) {
	return wf.Run{}, errors.New("not implemented")
}
func (*p5WorkflowEngineStub) GetRun(context.Context, string) (wf.Run, error) {
	return wf.Run{}, wf.ErrNotFound
}
func (*p5WorkflowEngineStub) ListRuns(context.Context, string, int) ([]wf.Run, error) {
	return nil, nil
}
func (*p5WorkflowEngineStub) Resume(context.Context, string, bool, string) (wf.Run, error) {
	return wf.Run{}, errors.New("not implemented")
}
func (*p5WorkflowEngineStub) ResumeQueued(context.Context, string, bool, string, int) (wf.Run, error) {
	return wf.Run{}, errors.New("not implemented")
}
func (*p5WorkflowEngineStub) Events(context.Context, string, int64, int) ([]wf.Event, error) {
	return nil, nil
}
func (*p5WorkflowEngineStub) NodeObservations(context.Context, string) ([]wf.NodeObservation, error) {
	return nil, nil
}
func (*p5WorkflowEngineStub) RecoverNode(context.Context, string, string, string, string, int) (wf.Run, error) {
	return wf.Run{}, errors.New("not implemented")
}

func TestP5WorkflowDefinitionJSONYAMLAndGraphStayConsistent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := &p5WorkflowEngineStub{definitions: make(map[string]wf.Definition)}
	router := gin.New()
	NewWorkflowHandler(engine).RegisterRoutes(router.Group("/api/v1"))

	original := wf.Definition{
		ID: "p5-api-workflow", Name: "P5 API workflow", Version: 1, Entry: "draft",
		Nodes: []wf.Node{
			{ID: "draft", Type: wf.NodeAgent, Agent: &wf.AgentConfig{ModelID: "model-test", WorkspaceID: "workspace-test", Prompt: "draft"}},
			{ID: "review", Type: wf.NodeAgent, Agent: &wf.AgentConfig{ModelID: "model-test", WorkspaceID: "workspace-test", Prompt: "review"}},
			{ID: "end", Type: wf.NodeEnd},
		},
		Edges: []wf.Edge{{From: "draft", To: "review"}, {From: "review", To: "end"}},
	}
	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	put := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workflows/"+original.ID, bytesReader(payload))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(put, req)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT definition status=%d body=%s", put.Code, put.Body.String())
	}

	yamlResponse := httptest.NewRecorder()
	router.ServeHTTP(yamlResponse, httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+original.ID+"/yaml", nil))
	if yamlResponse.Code != http.StatusOK {
		t.Fatalf("GET YAML status=%d body=%s", yamlResponse.Code, yamlResponse.Body.String())
	}
	fromYAML, err := yamlcodec.Import(yamlResponse.Body.Bytes())
	if err != nil {
		t.Fatalf("parse API YAML: %v", err)
	}
	if !reflect.DeepEqual(fromYAML, original) {
		t.Fatalf("API YAML changed workflow: %#v", fromYAML)
	}

	graphResponse := httptest.NewRecorder()
	router.ServeHTTP(graphResponse, httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+original.ID+"/graph", nil))
	if graphResponse.Code != http.StatusOK {
		t.Fatalf("GET graph status=%d body=%s", graphResponse.Code, graphResponse.Body.String())
	}
	var graph yamlcodec.Graph
	if err := json.Unmarshal(graphResponse.Body.Bytes(), &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 3 || len(graph.Edges) != 2 {
		t.Fatalf("unexpected graph dimensions: nodes=%d edges=%d", len(graph.Nodes), len(graph.Edges))
	}

	graphPutBody, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	graphPut := httptest.NewRecorder()
	graphReq := httptest.NewRequest(http.MethodPut, "/api/v1/workflows/"+original.ID+"/graph", bytesReader(graphPutBody))
	graphReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(graphPut, graphReq)
	if graphPut.Code != http.StatusOK {
		t.Fatalf("PUT graph status=%d body=%s", graphPut.Code, graphPut.Body.String())
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+original.ID, nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET definition status=%d body=%s", get.Code, get.Body.String())
	}
	var got wf.Definition
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("graph save changed canonical workflow: %#v", got)
	}
}

func bytesReader(data []byte) *bytes.Reader { return bytes.NewReader(data) }
