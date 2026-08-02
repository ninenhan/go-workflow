package units

import (
	"context"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestGetDictionaryValueUnitReturnsConfiguredValue(t *testing.T) {
	action := NewGetDictionaryValueUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{
		Input:  &unit.Input{Data: map[string]any{"name": "Ada", "empty": nil}},
		Params: map[string]any{"key": " name "},
	})
	if err != nil {
		t.Fatalf("execute GetDictionaryValueUnit: %v", err)
	}
	if result.Data != "Ada" {
		t.Fatalf("unexpected dictionary value: %#v", result.Data)
	}
}

func TestGetDictionaryValueUnitPreservesPresentNull(t *testing.T) {
	action := NewGetDictionaryValueUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{
		Input:  &unit.Input{Data: map[string]any{"empty": nil}},
		Params: map[string]any{"key": "empty"},
	})
	if err != nil {
		t.Fatalf("execute null dictionary value: %v", err)
	}
	if result.Data != nil {
		t.Fatalf("expected nil dictionary value, got %#v", result.Data)
	}
}

func TestGetDictionaryValueUnitRejectsInvalidInputAndKey(t *testing.T) {
	action := NewGetDictionaryValueUnit()
	tests := []struct {
		name    string
		node    *unit.Node
		message string
	}{
		{name: "missing key", node: &unit.Node{Input: &unit.Input{Data: map[string]any{"name": "Ada"}}}, message: "key is required"},
		{name: "missing input", node: &unit.Node{Params: map[string]any{"key": "name"}}, message: "missing dictionary input"},
		{name: "scalar input", node: &unit.Node{Input: &unit.Input{Data: "Ada"}, Params: map[string]any{"key": "name"}}, message: "input must be a dictionary"},
		{name: "missing dictionary key", node: &unit.Node{Input: &unit.Input{Data: map[string]any{"name": "Ada"}}, Params: map[string]any{"key": "score"}}, message: "does not exist"},
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
