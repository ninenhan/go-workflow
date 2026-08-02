package units

import (
	"context"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestTextUnitCreatesText(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "empty", want: ""},
		{name: "fixed text", value: "Hello Ada", want: "Hello Ada"},
		{name: "number", value: 42.5, want: "42.5"},
		{name: "boolean", value: true, want: "true"},
		{name: "structured value", value: map[string]any{"active": true, "name": "Ada"}, want: `{"active":true,"name":"Ada"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := NewTextUnit()
			result, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{"text": test.value}})
			if err != nil {
				t.Fatalf("execute TextUnit: %v", err)
			}
			if result.Data != test.want {
				t.Fatalf("unexpected text: got %#v want %#v", result.Data, test.want)
			}
		})
	}
}

func TestTextUnitRejectsInvalidValues(t *testing.T) {
	action := NewTextUnit()
	_, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{"text": make(chan string)}})
	if err == nil || !strings.Contains(err.Error(), "encode text value") {
		t.Fatalf("expected encoding error, got %v", err)
	}
}

func TestTextUnitRequiresNode(t *testing.T) {
	action := NewTextUnit()
	_, err := action.Execute(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "missing node") {
		t.Fatalf("expected missing node error, got %v", err)
	}
}
