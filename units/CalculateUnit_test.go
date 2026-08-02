package units

import (
	"context"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func executeCalculation(t *testing.T, left any, operation string, right any) (float64, error) {
	t.Helper()
	action := NewCalculateUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"left": left, "operation": operation, "right": right,
	}})
	if err != nil {
		return 0, err
	}
	return result.Data.(float64), nil
}

func TestCalculateUnitOperations(t *testing.T) {
	tests := []struct {
		name      string
		left      any
		operation string
		right     any
		want      float64
	}{
		{name: "add", left: 8, operation: "add", right: 2, want: 10},
		{name: "default operation", left: 8, right: 2, want: 10},
		{name: "subtract", left: 8, operation: "subtract", right: 2, want: 6},
		{name: "multiply", left: "8", operation: "multiply", right: 2.5, want: 20},
		{name: "divide", left: 8, operation: "divide", right: 2, want: 4},
		{name: "remainder", left: 8, operation: "remainder", right: 3, want: 2},
		{name: "power", left: 2, operation: "power", right: 3, want: 8},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := executeCalculation(t, test.left, test.operation, test.right)
			if err != nil {
				t.Fatalf("execute CalculateUnit: %v", err)
			}
			if got != test.want {
				t.Fatalf("unexpected result: got %v want %v", got, test.want)
			}
		})
	}
}

func TestCalculateUnitRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		node    *unit.Node
		message string
	}{
		{name: "missing node", message: "missing node"},
		{name: "missing left", node: &unit.Node{Params: map[string]any{"right": 1}}, message: "left must be a number"},
		{name: "boolean", node: &unit.Node{Params: map[string]any{"left": true, "right": 1}}, message: "left must be a number"},
		{name: "divide by zero", node: &unit.Node{Params: map[string]any{"left": 1, "operation": "divide", "right": 0}}, message: "divide by zero"},
		{name: "zero remainder", node: &unit.Node{Params: map[string]any{"left": 1, "operation": "remainder", "right": 0}}, message: "remainder with zero"},
		{name: "non finite result", node: &unit.Node{Params: map[string]any{"left": 1e308, "operation": "multiply", "right": 1e308}}, message: "non-finite result"},
		{name: "unknown operation", node: &unit.Node{Params: map[string]any{"left": 1, "operation": "root", "right": 2}}, message: "unsupported operation"},
	}
	action := NewCalculateUnit()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := action.Execute(context.Background(), nil, test.node)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}
