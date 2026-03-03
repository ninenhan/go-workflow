package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ComposerDocument stores workflow definition plus optional UI layout metadata.
// Layout is intentionally kept as opaque JSON to avoid tight coupling with the UI.
type ComposerDocument struct {
	Definition *WorkflowDefinition `json:"definition"`
	Layout     json.RawMessage     `json:"layout,omitempty"`
}

func ParseComposerJSON(data []byte) (*ComposerDocument, error) {
	if len(data) == 0 {
		return nil, errors.New("empty json")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	if _, ok := probe["definition"]; ok {
		var doc ComposerDocument
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		if doc.Definition == nil {
			return nil, errors.New("definition is required")
		}
		// Allow callers to send layout as null.
		if len(doc.Layout) > 0 && string(doc.Layout) == "null" {
			doc.Layout = nil
		}
		return &doc, nil
	}

	def, err := ParseWorkflowJSON(data)
	if err != nil {
		return nil, err
	}
	return &ComposerDocument{Definition: def}, nil
}

// LayoutValidator can validate layout structure against a workflow definition.
// Layout is provided as a generic JSON object to keep storage flexible.
type LayoutValidator interface {
	Validate(def *WorkflowDefinition, layout map[string]any) error
}

type LayoutValidatorFunc func(def *WorkflowDefinition, layout map[string]any) error

func (f LayoutValidatorFunc) Validate(def *WorkflowDefinition, layout map[string]any) error {
	return f(def, layout)
}

// ValidateBasic validates the definition and (optionally) the layout.
func (d *ComposerDocument) ValidateBasic(layoutValidator LayoutValidator) error {
	if d == nil {
		return errors.New("composer document is nil")
	}
	if d.Definition == nil {
		return errors.New("definition is required")
	}
	if err := d.Definition.ValidateBasic(); err != nil {
		return err
	}
	if layoutValidator == nil || len(d.Layout) == 0 {
		return nil
	}
	var layout map[string]any
	if err := json.Unmarshal(d.Layout, &layout); err != nil {
		return fmt.Errorf("invalid layout json: %w", err)
	}
	return layoutValidator.Validate(d.Definition, layout)
}

// ValidateLayoutBasic performs a minimal structural check for layout JSON.
// Accepted forms:
// 1) { "<nodeId>": { "x": number, "y": number, ... }, ... }
// 2) { "nodes": { "<nodeId>": { "x": number, "y": number } }, "subflows": { "<whileId>": { "nodes": {...} } } }
// Subflow keys must refer to WhileUnit nodes.
func ValidateLayoutBasic(def *WorkflowDefinition, layout map[string]any) error {
	if def == nil {
		return errors.New("definition is required")
	}
	if layout == nil {
		return nil
	}
	if nodesRaw, ok := layout["nodes"]; ok {
		nodeMap, ok := nodesRaw.(map[string]any)
		if !ok {
			return errors.New("layout.nodes must be an object")
		}
		if err := validateNodePositions(nodeMap); err != nil {
			return fmt.Errorf("layout.nodes: %w", err)
		}
	} else {
		// Treat the top-level map as node positions.
		if err := validateNodePositions(layout); err != nil {
			return fmt.Errorf("layout: %w", err)
		}
	}

	if subflowsRaw, ok := layout["subflows"]; ok {
		subflows, ok := subflowsRaw.(map[string]any)
		if !ok {
			return errors.New("layout.subflows must be an object")
		}
		for whileID, subflowRaw := range subflows {
			nodeSpec := def.Nodes[whileID]
			if nodeSpec == nil {
				return fmt.Errorf("layout.subflows key %s not found in definition", whileID)
			}
			unit := nodeSpec.Unit
			if unit == "" {
				unit = nodeSpec.UnitID
			}
			if unit != "WhileUnit" {
				return fmt.Errorf("layout.subflows key %s is not a WhileUnit", whileID)
			}
			subflowMap, ok := subflowRaw.(map[string]any)
			if !ok {
				return fmt.Errorf("layout.subflows.%s must be an object", whileID)
			}
			if nodesRaw, ok := subflowMap["nodes"]; ok {
				nodeMap, ok := nodesRaw.(map[string]any)
				if !ok {
					return fmt.Errorf("layout.subflows.%s.nodes must be an object", whileID)
				}
				if err := validateNodePositions(nodeMap); err != nil {
					return fmt.Errorf("layout.subflows.%s.nodes: %w", whileID, err)
				}
			} else {
				if err := validateNodePositions(subflowMap); err != nil {
					return fmt.Errorf("layout.subflows.%s: %w", whileID, err)
				}
			}
		}
	}

	return nil
}

func validateNodePositions(m map[string]any) error {
	for key, raw := range m {
		obj, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("node %s must be an object", key)
		}
		if _, ok := obj["x"]; !ok {
			return fmt.Errorf("node %s missing x", key)
		}
		if _, ok := obj["y"]; !ok {
			return fmt.Errorf("node %s missing y", key)
		}
		if !isNumber(obj["x"]) || !isNumber(obj["y"]) {
			return fmt.Errorf("node %s x/y must be numbers", key)
		}
	}
	return nil
}

func isNumber(v any) bool {
	switch v.(type) {
	case int, int32, int64, float32, float64, json.Number:
		return true
	default:
		return false
	}
}
