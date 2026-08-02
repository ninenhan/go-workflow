package units

import (
	"context"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func executeCount(t *testing.T, input any, mode string) (int, error) {
	t.Helper()
	params := map[string]any{}
	if mode != "" {
		params["count"] = mode
	}
	action := NewCountUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{
		Input: &unit.Input{Data: input}, Params: params,
	})
	if err != nil {
		return 0, err
	}
	return result.Data.(int), nil
}

func TestCountUnitModes(t *testing.T) {
	tests := []struct {
		name  string
		input any
		mode  string
		want  int
	}{
		{name: "list items by default", input: []any{"a", "b", "c"}, want: 3},
		{name: "array items", input: [2]string{"a", "b"}, want: 2},
		{name: "dictionary items", input: map[string]any{"a": 1, "b": 2}, want: 2},
		{name: "unicode characters", input: "A世🙂", mode: "characters", want: 3},
		{name: "unicode words", input: "Hello 世界 42 don't-stop", mode: "words", want: 5},
		{name: "normalized lines", input: "one\r\ntwo\rthree", mode: "lines", want: 3},
		{name: "empty text lines", input: "", mode: "lines", want: 0},
		{name: "trailing empty line", input: "one\n", mode: "lines", want: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := executeCount(t, test.input, test.mode)
			if err != nil {
				t.Fatalf("execute CountUnit: %v", err)
			}
			if got != test.want {
				t.Fatalf("unexpected count: got %d want %d", got, test.want)
			}
		})
	}
}

func TestCountUnitRejectsInvalidInputAndMode(t *testing.T) {
	tests := []struct {
		name    string
		node    *unit.Node
		message string
	}{
		{name: "missing input", node: &unit.Node{}, message: "missing input"},
		{name: "text items", node: &unit.Node{Input: &unit.Input{Data: "text"}}, message: "items mode requires"},
		{name: "bytes items", node: &unit.Node{Input: &unit.Input{Data: []byte("text")}}, message: "items mode requires"},
		{name: "list characters", node: &unit.Node{Input: &unit.Input{Data: []any{"text"}}, Params: map[string]any{"count": "characters"}}, message: "requires text input"},
		{name: "unknown mode", node: &unit.Node{Input: &unit.Input{Data: "text"}, Params: map[string]any{"count": "bytes"}}, message: "unsupported count"},
	}
	action := NewCountUnit()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := action.Execute(context.Background(), nil, test.node)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}
