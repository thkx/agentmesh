package compiler

import (
	"fmt"
	"github.com/thkx/agentmesh/internal/domain/workflow"
)

type Compiled struct {
	Definition workflow.Definition
	Nodes      map[string]workflow.Node
	Outgoing   map[string][]workflow.Edge
}

func Compile(d workflow.Definition) (*Compiled, error) {
	if err := d.ValidateShape(); err != nil {
		return nil, err
	}
	c := &Compiled{Definition: d, Nodes: map[string]workflow.Node{}, Outgoing: map[string][]workflow.Edge{}}
	startCount := 0
	for _, n := range d.Nodes {
		if n.ID == "" {
			return nil, fmt.Errorf("%w: node id is empty", workflow.ErrInvalidDefinition)
		}
		if _, ok := c.Nodes[n.ID]; ok {
			return nil, fmt.Errorf("%w: duplicate node %q", workflow.ErrInvalidDefinition, n.ID)
		}
		switch n.Type {
		case workflow.NodeStart:
			startCount++
			if n.ID != d.Entry {
				return nil, fmt.Errorf("%w: start node %s must be the entry", workflow.ErrInvalidDefinition, n.ID)
			}
		case workflow.NodeAgent:
			if n.Agent == nil || n.Agent.ModelID == "" || n.Agent.WorkspaceID == "" || n.Agent.Prompt == "" {
				return nil, fmt.Errorf("%w: agent node %s requires model_id, workspace_id and prompt", workflow.ErrInvalidDefinition, n.ID)
			}
		case workflow.NodeCondition:
			if n.Condition == nil || n.Condition.InputKey == "" {
				return nil, fmt.Errorf("%w: condition node %s requires input_key", workflow.ErrInvalidDefinition, n.ID)
			}
		case workflow.NodeApproval:
			if n.Approval == nil {
				return nil, fmt.Errorf("%w: approval node %s requires approval config", workflow.ErrInvalidDefinition, n.ID)
			}
		case workflow.NodeEnd:
		default:
			return nil, fmt.Errorf("%w: unsupported node type %q", workflow.ErrInvalidDefinition, n.Type)
		}
		c.Nodes[n.ID] = n
	}
	if startCount > 1 {
		return nil, fmt.Errorf("%w: workflow can contain only one start node", workflow.ErrInvalidDefinition)
	}
	if _, ok := c.Nodes[d.Entry]; !ok {
		return nil, fmt.Errorf("%w: entry node does not exist", workflow.ErrInvalidDefinition)
	}
	for _, e := range d.Edges {
		if _, ok := c.Nodes[e.From]; !ok {
			return nil, fmt.Errorf("%w: edge source %s missing", workflow.ErrInvalidDefinition, e.From)
		}
		if _, ok := c.Nodes[e.To]; !ok {
			return nil, fmt.Errorf("%w: edge target %s missing", workflow.ErrInvalidDefinition, e.To)
		}
		c.Outgoing[e.From] = append(c.Outgoing[e.From], e)
	}
	for id, n := range c.Nodes {
		out := c.Outgoing[id]
		if n.Type == workflow.NodeEnd && len(out) > 0 {
			return nil, fmt.Errorf("%w: end node %s cannot have outgoing edges", workflow.ErrInvalidDefinition, id)
		}
		if n.Type != workflow.NodeEnd && len(out) == 0 {
			return nil, fmt.Errorf("%w: non-end node %s has no outgoing edge", workflow.ErrInvalidDefinition, id)
		}
		if n.Type == workflow.NodeCondition {
			t, f := false, false
			for _, e := range out {
				if e.Handle == "true" {
					if t {
						return nil, fmt.Errorf("%w: duplicate true edge on %s", workflow.ErrInvalidDefinition, id)
					}
					t = true
				}
				if e.Handle == "false" {
					if f {
						return nil, fmt.Errorf("%w: duplicate false edge on %s", workflow.ErrInvalidDefinition, id)
					}
					f = true
				}
				if e.Handle != "true" && e.Handle != "false" {
					return nil, fmt.Errorf("%w: invalid condition handle on %s", workflow.ErrInvalidDefinition, id)
				}
			}
			if !t || !f || len(out) != 2 {
				return nil, fmt.Errorf("%w: condition node %s needs exactly true and false edges", workflow.ErrInvalidDefinition, id)
			}
		} else {
			for _, e := range out {
				if e.Handle != "" {
					return nil, fmt.Errorf("%w: only condition edges may have handles", workflow.ErrInvalidDefinition)
				}
			}
		}
	}
	// All nodes must be reachable from entry; cycles are rejected in M8's deterministic execution model.
	color := map[string]int{}
	reachable := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if color[id] == 1 {
			return fmt.Errorf("%w: cycle detected at %s", workflow.ErrInvalidDefinition, id)
		}
		if color[id] == 2 {
			return nil
		}
		color[id] = 1
		reachable[id] = true
		for _, e := range c.Outgoing[id] {
			if err := visit(e.To); err != nil {
				return err
			}
		}
		color[id] = 2
		return nil
	}
	if err := visit(d.Entry); err != nil {
		return nil, err
	}
	for id := range c.Nodes {
		if !reachable[id] {
			return nil, fmt.Errorf("%w: unreachable node %s", workflow.ErrInvalidDefinition, id)
		}
	}
	return c, nil
}
