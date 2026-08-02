package units

import (
	"context"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func executeReplaceText(t *testing.T, input string, params map[string]any) (string, error) {
	t.Helper()
	action := NewReplaceTextUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{
		Input: &unit.Input{Data: input}, Params: params,
	})
	if err != nil {
		return "", err
	}
	return result.Data.(string), nil
}

func TestReplaceTextUnitLiteralModes(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		params map[string]any
		want   string
	}{
		{
			name: "replace all by default", input: "cat cat",
			params: map[string]any{"find": "cat", "replacement": "dog"},
			want:   "dog dog",
		},
		{
			name: "replace first", input: "cat cat",
			params: map[string]any{"find": "cat", "replacement": "dog", "replace_all": false},
			want:   "dog cat",
		},
		{
			name: "case insensitive unicode", input: "Äpfel äPFEL",
			params: map[string]any{"find": "äpfel", "replacement": "fruit", "case_sensitive": false},
			want:   "fruit fruit",
		},
		{
			name: "case sensitive by default", input: "CAT cat",
			params: map[string]any{"find": "cat", "replacement": "dog"},
			want:   "CAT dog",
		},
		{
			name: "literal metacharacters and replacement", input: "a.b a.b",
			params: map[string]any{"find": "a.b", "replacement": "$value"},
			want:   "$value $value",
		},
		{
			name: "remove matches", input: "alpha-beta",
			params: map[string]any{"find": "-", "replacement": ""},
			want:   "alphabeta",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := executeReplaceText(t, test.input, test.params)
			if err != nil {
				t.Fatalf("execute ReplaceTextUnit: %v", err)
			}
			if got != test.want {
				t.Fatalf("unexpected replacement: got %q want %q", got, test.want)
			}
		})
	}
}

func TestReplaceTextUnitRejectsInvalidInputAndConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		node    *unit.Node
		message string
	}{
		{name: "missing input", node: &unit.Node{}, message: "missing text input"},
		{name: "list input", node: &unit.Node{Input: &unit.Input{Data: []any{"cat"}}, Params: map[string]any{"find": "cat"}}, message: "input must be text"},
		{name: "empty find", node: &unit.Node{Input: &unit.Input{Data: "cat"}, Params: map[string]any{}}, message: "find must not be empty"},
		{name: "non text find", node: &unit.Node{Input: &unit.Input{Data: "cat"}, Params: map[string]any{"find": 1}}, message: "find must be text"},
		{name: "invalid switch", node: &unit.Node{Input: &unit.Input{Data: "cat"}, Params: map[string]any{"find": "cat", "replace_all": 1}}, message: "replace_all must be a boolean"},
	}
	action := NewReplaceTextUnit()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := action.Execute(context.Background(), nil, test.node)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}
