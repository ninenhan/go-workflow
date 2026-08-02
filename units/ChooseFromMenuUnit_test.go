package units

import (
	"context"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestChooseFromMenuUnitReturnsSelectedItem(t *testing.T) {
	action := NewChooseFromMenuUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"items":  []any{"Alpha", "Beta", "Gamma"},
		"choice": "Beta",
	}})
	if err != nil {
		t.Fatalf("choose menu item: %v", err)
	}
	if result.Data != "Beta" {
		t.Fatalf("unexpected menu result: %#v", result.Data)
	}
}

func TestChooseFromMenuUnitDefaultsToFirstItem(t *testing.T) {
	action := NewChooseFromMenuUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"items": []string{"First", "Second"},
	}})
	if err != nil {
		t.Fatalf("choose default menu item: %v", err)
	}
	if result.Data != "First" {
		t.Fatalf("unexpected default menu result: %#v", result.Data)
	}
}

func TestChooseFromMenuUnitRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		node    *unit.Node
		message string
	}{
		{name: "missing items", node: &unit.Node{}, message: "menu items are required"},
		{name: "empty items", node: &unit.Node{Params: map[string]any{"items": []any{}}}, message: "menu items are required"},
		{name: "non-list items", node: &unit.Node{Params: map[string]any{"items": "Alpha"}}, message: "must be a list of text"},
		{name: "blank item", node: &unit.Node{Params: map[string]any{"items": []any{"Alpha", " "}}}, message: "item 2 must be non-empty"},
		{name: "non-text item", node: &unit.Node{Params: map[string]any{"items": []any{"Alpha", 2}}}, message: "item 2 must be non-empty text"},
		{name: "duplicate item", node: &unit.Node{Params: map[string]any{"items": []any{"Alpha", "Alpha"}}}, message: "is duplicated"},
		{name: "unknown choice", node: &unit.Node{Params: map[string]any{"items": []any{"Alpha", "Beta"}, "choice": "Gamma"}}, message: "is not in the configured menu"},
		{name: "non-text choice", node: &unit.Node{Params: map[string]any{"items": []any{"Alpha"}, "choice": 1}}, message: "choice must be non-empty text"},
	}
	action := NewChooseFromMenuUnit()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := action.Execute(context.Background(), nil, test.node)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}
