package yamlcodec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/workflow/compiler"
	"go.yaml.in/yaml/v3"
	"io"
)

var ErrInvalidYAML = errors.New("invalid workflow YAML")

func Import(data []byte) (workflow.Definition, error) {
	if len(data) == 0 || len(data) > 2<<20 {
		return workflow.Definition{}, ErrInvalidYAML
	}
	var raw any
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&raw); err != nil {
		return workflow.Definition{}, fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return workflow.Definition{}, fmt.Errorf("%w: multiple YAML documents are not supported", ErrInvalidYAML)
	}
	if err := validateDefinitionKeys(raw); err != nil {
		return workflow.Definition{}, err
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return workflow.Definition{}, fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}
	var d workflow.Definition
	if err = json.Unmarshal(b, &d); err != nil {
		return d, fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}
	if _, err = compiler.Compile(d); err != nil {
		return d, err
	}
	return d, nil
}
func validateDefinitionKeys(raw any) error {
	allowed := map[string]bool{"id": true, "name": true, "version": true, "entry": true, "nodes": true, "edges": true, "created_at": true}
	root, ok := raw.(map[string]any)
	if !ok {
		return ErrInvalidYAML
	}
	for key := range root {
		if !allowed[key] {
			return fmt.Errorf("%w: unknown workflow field %q", ErrInvalidYAML, key)
		}
	}
	nodeAllowed := map[string]bool{"id": true, "type": true, "name": true, "agent": true, "condition": true, "approval": true, "subworkflow": true}
	for _, item := range asSlice(root["nodes"]) {
		node, ok := item.(map[string]any)
		if !ok {
			return ErrInvalidYAML
		}
		for key := range node {
			if !nodeAllowed[key] {
				return fmt.Errorf("%w: unknown node field %q", ErrInvalidYAML, key)
			}
		}
	}
	edgeAllowed := map[string]bool{"from": true, "to": true, "handle": true}
	for _, item := range asSlice(root["edges"]) {
		edge, ok := item.(map[string]any)
		if !ok {
			return ErrInvalidYAML
		}
		for key := range edge {
			if !edgeAllowed[key] {
				return fmt.Errorf("%w: unknown edge field %q", ErrInvalidYAML, key)
			}
		}
	}
	return nil
}
func asSlice(v any) []any {
	if v == nil {
		return nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	return items
}

func Export(d workflow.Definition) ([]byte, error) {
	if _, err := compiler.Compile(d); err != nil {
		return nil, err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var raw any
	if err = json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	return yaml.Marshal(raw)
}

type GraphNode struct {
	ID       string            `json:"id"`
	Type     workflow.NodeType `json:"type"`
	Name     string            `json:"name,omitempty"`
	Position Position          `json:"position"`
	Config   json.RawMessage   `json:"config,omitempty"`
}
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type GraphEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Handle string `json:"handle,omitempty"`
}
type Graph struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Version int         `json:"version"`
	Entry   string      `json:"entry"`
	Nodes   []GraphNode `json:"nodes"`
	Edges   []GraphEdge `json:"edges"`
}

func ToGraph(d workflow.Definition) (Graph, error) {
	g := Graph{ID: d.ID, Name: d.Name, Version: d.Version, Entry: d.Entry, Nodes: make([]GraphNode, 0, len(d.Nodes)), Edges: make([]GraphEdge, 0, len(d.Edges))}
	for i, n := range d.Nodes {
		cfg, err := json.Marshal(struct {
			Agent       any `json:"agent,omitempty"`
			Condition   any `json:"condition,omitempty"`
			Approval    any `json:"approval,omitempty"`
			Subworkflow any `json:"subworkflow,omitempty"`
		}{n.Agent, n.Condition, n.Approval, n.Subworkflow})
		if err != nil {
			return g, err
		}
		g.Nodes = append(g.Nodes, GraphNode{ID: n.ID, Type: n.Type, Name: n.Name, Position: Position{X: float64((i % 4) * 240), Y: float64((i / 4) * 140)}, Config: cfg})
	}
	for i, e := range d.Edges {
		g.Edges = append(g.Edges, GraphEdge{ID: fmt.Sprintf("edge-%d", i+1), Source: e.From, Target: e.To, Handle: e.Handle})
	}
	return g, nil
}
func FromGraph(g Graph) (workflow.Definition, error) {
	d := workflow.Definition{ID: g.ID, Name: g.Name, Version: g.Version, Entry: g.Entry, Nodes: make([]workflow.Node, 0, len(g.Nodes)), Edges: make([]workflow.Edge, 0, len(g.Edges))}
	for _, n := range g.Nodes {
		node := workflow.Node{ID: n.ID, Type: n.Type, Name: n.Name}
		if len(n.Config) > 0 {
			var cfg struct {
				Agent       *workflow.AgentConfig       `json:"agent"`
				Condition   *workflow.ConditionConfig   `json:"condition"`
				Approval    *workflow.ApprovalConfig    `json:"approval"`
				Subworkflow *workflow.SubworkflowConfig `json:"subworkflow"`
			}
			if err := json.Unmarshal(n.Config, &cfg); err != nil {
				return d, err
			}
			node.Agent, node.Condition, node.Approval, node.Subworkflow = cfg.Agent, cfg.Condition, cfg.Approval, cfg.Subworkflow
		}
		d.Nodes = append(d.Nodes, node)
	}
	for _, e := range g.Edges {
		d.Edges = append(d.Edges, workflow.Edge{From: e.Source, To: e.Target, Handle: e.Handle})
	}
	if _, err := compiler.Compile(d); err != nil {
		return d, err
	}
	return d, nil
}
