package units

import (
	"context"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func executeGetListItem(t *testing.T, operation string, params map[string]any) (any, error) {
	t.Helper()
	action := NewGetListItemUnit()
	if params == nil {
		params = map[string]any{}
	}
	params["operation"] = operation
	result, err := action.Execute(context.Background(), nil, &unit.Node{
		Input:  &unit.Input{Data: []any{"alpha", "beta", "gamma"}},
		Params: params,
	})
	if err != nil {
		return nil, err
	}
	return result.Data, nil
}

func TestGetListItemUnitSelectionModes(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		params    map[string]any
		want      string
	}{
		{name: "first", operation: "first", want: "alpha"},
		{name: "last", operation: "last", want: "gamma"},
		{name: "index", operation: "index", params: map[string]any{"item_number": 2}, want: "beta"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := executeGetListItem(t, test.operation, test.params)
			if err != nil {
				t.Fatalf("execute GetListItemUnit: %v", err)
			}
			if value != test.want {
				t.Fatalf("unexpected selected item: got %#v want %q", value, test.want)
			}
		})
	}
}

func TestGetListItemUnitRandomReturnsMember(t *testing.T) {
	value, err := executeGetListItem(t, "random", nil)
	if err != nil {
		t.Fatalf("execute random GetListItemUnit: %v", err)
	}
	if value != "alpha" && value != "beta" && value != "gamma" {
		t.Fatalf("random mode returned unknown item: %#v", value)
	}
}

func TestGetListItemUnitRejectsInvalidInputAndSelection(t *testing.T) {
	action := NewGetListItemUnit()
	tests := []struct {
		name    string
		node    *unit.Node
		message string
	}{
		{name: "missing input", node: &unit.Node{}, message: "missing list input"},
		{name: "scalar input", node: &unit.Node{Input: &unit.Input{Data: "alpha"}}, message: "input must be a list"},
		{name: "empty input", node: &unit.Node{Input: &unit.Input{Data: []any{}}}, message: "input list is empty"},
		{name: "missing index", node: &unit.Node{Input: &unit.Input{Data: []any{"alpha"}}, Params: map[string]any{"operation": "index"}}, message: "item_number is required"},
		{name: "fractional index", node: &unit.Node{Input: &unit.Input{Data: []any{"alpha"}}, Params: map[string]any{"operation": "index", "item_number": 1.5}}, message: "positive integer"},
		{name: "out of range", node: &unit.Node{Input: &unit.Input{Data: []any{"alpha"}}, Params: map[string]any{"operation": "index", "item_number": 2}}, message: "exceeds list length"},
		{name: "unknown operation", node: &unit.Node{Input: &unit.Input{Data: []any{"alpha"}}, Params: map[string]any{"operation": "middle"}}, message: "unsupported operation"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := action.Execute(context.Background(), nil, test.node)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}
