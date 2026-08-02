package units

import (
	"context"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestChangeCaseUnitModes(t *testing.T) {
	tests := []struct {
		name string
		mode string
		text string
		want string
	}{
		{name: "upper by default", text: "Hello 世界", want: "HELLO 世界"},
		{name: "lower", mode: "lower", text: "Hello 世界", want: "hello 世界"},
		{name: "title", mode: "title", text: "hELLO, wORLD! don't STOP", want: "Hello, World! Don't Stop"},
		{name: "title hyphen", mode: "title", text: "state-of-the-art", want: "State-Of-The-Art"},
		{name: "sentence", mode: "sentence", text: "hELLO. wORLD! AGAIN? yes", want: "Hello. World! Again? Yes"},
		{name: "sentence quote", mode: "sentence", text: `"hELLO," SHE SAID.`, want: `"Hello," she said.`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := NewChangeCaseUnit()
			params := map[string]any{}
			if test.mode != "" {
				params["case"] = test.mode
			}
			result, err := action.Execute(context.Background(), nil, &unit.Node{
				Input: &unit.Input{Data: test.text}, Params: params,
			})
			if err != nil {
				t.Fatalf("execute ChangeCaseUnit: %v", err)
			}
			if result.Data != test.want {
				t.Fatalf("unexpected case conversion: got %q want %q", result.Data, test.want)
			}
		})
	}
}

func TestChangeCaseUnitRejectsInvalidInputAndMode(t *testing.T) {
	tests := []struct {
		name    string
		node    *unit.Node
		message string
	}{
		{name: "missing input", node: &unit.Node{}, message: "missing text input"},
		{name: "list input", node: &unit.Node{Input: &unit.Input{Data: []any{"text"}}}, message: "input must be text"},
		{name: "unknown mode", node: &unit.Node{Input: &unit.Input{Data: "text"}, Params: map[string]any{"case": "camel"}}, message: "unsupported case"},
	}
	action := NewChangeCaseUnit()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := action.Execute(context.Background(), nil, test.node)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}
