package units

import (
	"context"
	"reflect"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestSplitTextUnitSeparators(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		params map[string]any
		want   []any
	}{
		{name: "new lines by default", input: "alpha\r\nbeta\rgamma", want: []any{"alpha", "beta", "gamma"}},
		{name: "spaces", input: "alpha beta gamma", params: map[string]any{"separator_mode": "spaces"}, want: []any{"alpha", "beta", "gamma"}},
		{name: "custom", input: []byte("alpha::beta::gamma"), params: map[string]any{"separator_mode": "custom", "custom_separator": "::"}, want: []any{"alpha", "beta", "gamma"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := NewSplitTextUnit()
			result, err := action.Execute(context.Background(), nil, &unit.Node{
				Input: &unit.Input{Data: test.input}, Params: test.params,
			})
			if err != nil {
				t.Fatalf("execute SplitTextUnit: %v", err)
			}
			if !reflect.DeepEqual(result.Data, test.want) {
				t.Fatalf("unexpected parts: got %#v want %#v", result.Data, test.want)
			}
		})
	}
}

func TestCombineTextUnitSeparators(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		params map[string]any
		want   string
	}{
		{name: "new lines by default", input: []any{"alpha", "beta"}, want: "alpha\nbeta"},
		{name: "spaces", input: []string{"alpha", "beta"}, params: map[string]any{"separator_mode": "spaces"}, want: "alpha beta"},
		{name: "custom", input: []any{"alpha", 2, true}, params: map[string]any{"separator_mode": "custom", "custom_separator": " · "}, want: "alpha · 2 · true"},
		{name: "empty list", input: []any{}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := NewCombineTextUnit()
			result, err := action.Execute(context.Background(), nil, &unit.Node{
				Input: &unit.Input{Data: test.input}, Params: test.params,
			})
			if err != nil {
				t.Fatalf("execute CombineTextUnit: %v", err)
			}
			if result.Data != test.want {
				t.Fatalf("unexpected text: got %#v want %#v", result.Data, test.want)
			}
		})
	}
}

func TestTextCollectionUnitsRejectInvalidInputAndSeparator(t *testing.T) {
	tests := []struct {
		name    string
		action  unit.ExecutableUnit
		node    *unit.Node
		message string
	}{
		{name: "split missing input", action: new(SplitTextUnit), node: &unit.Node{}, message: "missing text input"},
		{name: "split list input", action: new(SplitTextUnit), node: &unit.Node{Input: &unit.Input{Data: []any{"alpha"}}}, message: "input must be text"},
		{name: "combine missing input", action: new(CombineTextUnit), node: &unit.Node{}, message: "missing list input"},
		{name: "combine scalar input", action: new(CombineTextUnit), node: &unit.Node{Input: &unit.Input{Data: "alpha"}}, message: "input must be a list"},
		{name: "missing custom separator", action: new(SplitTextUnit), node: &unit.Node{Input: &unit.Input{Data: "alpha"}, Params: map[string]any{"separator_mode": "custom"}}, message: "custom_separator is required"},
		{name: "unknown mode", action: new(CombineTextUnit), node: &unit.Node{Input: &unit.Input{Data: []any{"alpha"}}, Params: map[string]any{"separator_mode": "tabs"}}, message: "unsupported separator_mode"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.action.Execute(context.Background(), nil, test.node)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}
