package units

import (
	"context"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestAskForInputUnitReturnsTypedAnswers(t *testing.T) {
	tests := []struct {
		name      string
		inputType string
		answer    any
		expected  any
	}{
		{name: "text", inputType: "text", answer: "Approved", expected: "Approved"},
		{name: "number", inputType: "number", answer: 42, expected: float64(42)},
		{name: "date", inputType: "date", answer: "2026-07-20", expected: "2026-07-20"},
		{name: "time", inputType: "time", answer: "09:35", expected: "09:35"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := NewAskForInputUnit()
			result, err := action.Execute(context.Background(), nil, &unit.Node{
				Params: map[string]any{"input_type": test.inputType},
				Input:  &unit.Input{Data: test.answer},
			})
			if err != nil {
				t.Fatalf("ask for input: %v", err)
			}
			if result.Data != test.expected {
				t.Fatalf("unexpected answer: got %#v want %#v", result.Data, test.expected)
			}
		})
	}
}

func TestAskForInputUnitRejectsInvalidAnswers(t *testing.T) {
	tests := []struct {
		name      string
		inputType string
		answer    any
		message   string
	}{
		{name: "blank text", inputType: "text", answer: " ", message: "non-empty text"},
		{name: "wrong number type", inputType: "number", answer: "42", message: "must be finite"},
		{name: "invalid date", inputType: "date", answer: "2026-02-30", message: "date answer is invalid"},
		{name: "invalid time", inputType: "time", answer: "25:00", message: "time answer is invalid"},
		{name: "unsupported type", inputType: "choice", answer: "A", message: "unsupported input_type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := NewAskForInputUnit()
			_, err := action.Execute(context.Background(), nil, &unit.Node{
				Params: map[string]any{"input_type": test.inputType},
				Input:  &unit.Input{Data: test.answer},
			})
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}

func TestAskForInputUnitRequiresAnswer(t *testing.T) {
	action := NewAskForInputUnit()
	_, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{"input_type": "text"}})
	if err == nil || !strings.Contains(err.Error(), "answer is required") {
		t.Fatalf("expected missing answer error, got %v", err)
	}
}
