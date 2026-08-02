package units

import (
	"context"
	"reflect"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestDictionaryUnitBuildsTypedValues(t *testing.T) {
	action := NewDictionaryUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"entries": []any{
			map[string]any{"key": "name", "type": "text", "value": "Ada"},
			map[string]any{"key": "score", "type": "number", "value": "42.5"},
			map[string]any{"key": "active", "type": "boolean", "value": true},
		},
	}})
	if err != nil {
		t.Fatalf("execute DictionaryUnit: %v", err)
	}
	want := map[string]any{"name": "Ada", "score": 42.5, "active": true}
	if !reflect.DeepEqual(result.Data, want) {
		t.Fatalf("unexpected dictionary: got %#v want %#v", result.Data, want)
	}
}

func TestDictionaryUnitWithoutEntriesReturnsEmptyDictionary(t *testing.T) {
	action := NewDictionaryUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{})
	if err != nil {
		t.Fatalf("execute empty DictionaryUnit: %v", err)
	}
	dictionary, ok := result.Data.(map[string]any)
	if !ok || len(dictionary) != 0 {
		t.Fatalf("expected empty dictionary, got %#v", result.Data)
	}
}

func TestDictionaryUnitRejectsInvalidEntries(t *testing.T) {
	action := NewDictionaryUnit()
	tests := []struct {
		name    string
		entries any
		message string
	}{
		{name: "scalar entries", entries: "name=Ada", message: "entries must be a list"},
		{name: "scalar entry", entries: []any{"name=Ada"}, message: "entry 1 must be an object"},
		{name: "blank key", entries: []any{map[string]any{"key": " ", "value": "Ada"}}, message: "key is required"},
		{name: "duplicate key", entries: []any{map[string]any{"key": "name", "value": "Ada"}, map[string]any{"key": "name", "value": "Grace"}}, message: "duplicate key"},
		{name: "bad number", entries: []any{map[string]any{"key": "score", "type": "number", "value": "many"}}, message: "finite number"},
		{name: "bad boolean", entries: []any{map[string]any{"key": "active", "type": "boolean", "value": "yes"}}, message: "true or false"},
		{name: "unknown type", entries: []any{map[string]any{"key": "payload", "type": "json", "value": "{}"}}, message: "unsupported type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{"entries": test.entries}})
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}
