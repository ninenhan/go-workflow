package units

import (
	"context"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestAdjustDateUnitUsesClampedCalendarArithmetic(t *testing.T) {
	action := NewAdjustDateUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{
		Input:  &unit.Input{Data: "2026-01-31T09:30:00+09:00"},
		Params: map[string]any{"operation": "add", "amount": 1, "unit": "month"},
	})
	if err != nil {
		t.Fatalf("adjust calendar month: %v", err)
	}
	if result.Data != "2026-02-28T09:30:00+09:00" {
		t.Fatalf("unexpected adjusted month: %#v", result.Data)
	}
}

func TestAdjustDateUnitUsesSelectedTimezoneAcrossDST(t *testing.T) {
	action := NewAdjustDateUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{
		Input:  &unit.Input{Data: "2026-03-07T09:30:00-05:00"},
		Params: map[string]any{"operation": "add", "amount": 1, "unit": "day", "timezone": "America/New_York"},
	})
	if err != nil {
		t.Fatalf("adjust across daylight saving time: %v", err)
	}
	if result.Data != "2026-03-08T09:30:00-04:00" {
		t.Fatalf("unexpected daylight saving result: %#v", result.Data)
	}
}

func TestAdjustDateUnitSubtractsDuration(t *testing.T) {
	action := NewAdjustDateUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{
		Input:  &unit.Input{Data: "2026-07-19T09:30:00+09:00"},
		Params: map[string]any{"operation": "subtract", "amount": 2, "unit": "hour"},
	})
	if err != nil {
		t.Fatalf("subtract duration: %v", err)
	}
	if result.Data != "2026-07-19T07:30:00+09:00" {
		t.Fatalf("unexpected duration result: %#v", result.Data)
	}
}

func TestAdjustDateUnitRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		node    *unit.Node
		message string
	}{
		{name: "missing input", node: &unit.Node{Params: map[string]any{"amount": 1}}, message: "missing date input"},
		{name: "invalid date", node: &unit.Node{Input: &unit.Input{Data: "tomorrow"}, Params: map[string]any{"amount": 1}}, message: "must be RFC 3339"},
		{name: "negative amount", node: &unit.Node{Input: &unit.Input{Data: "2026-07-19T09:30:00Z"}, Params: map[string]any{"amount": -1}}, message: "amount must be an integer"},
		{name: "fractional amount", node: &unit.Node{Input: &unit.Input{Data: "2026-07-19T09:30:00Z"}, Params: map[string]any{"amount": 1.5}}, message: "amount must be an integer"},
		{name: "unknown operation", node: &unit.Node{Input: &unit.Input{Data: "2026-07-19T09:30:00Z"}, Params: map[string]any{"amount": 1, "operation": "multiply"}}, message: "unsupported operation"},
		{name: "unknown unit", node: &unit.Node{Input: &unit.Input{Data: "2026-07-19T09:30:00Z"}, Params: map[string]any{"amount": 1, "unit": "fortnight"}}, message: "unsupported unit"},
		{name: "unknown timezone", node: &unit.Node{Input: &unit.Input{Data: "2026-07-19T09:30:00Z"}, Params: map[string]any{"amount": 1, "timezone": "Mars/Olympus"}}, message: "load timezone"},
	}
	action := NewAdjustDateUnit()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := action.Execute(context.Background(), nil, test.node)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}

func TestFormatDateUnitPresets(t *testing.T) {
	tests := []struct {
		format string
		want   string
	}{
		{format: "iso", want: "2026-07-19T09:30:45+09:00"},
		{format: "date", want: "2026-07-19"},
		{format: "time", want: "09:30:45"},
		{format: "date_time", want: "2026-07-19 09:30"},
		{format: "unix", want: "1784421045"},
	}
	action := NewFormatDateUnit()
	for _, test := range tests {
		t.Run(test.format, func(t *testing.T) {
			result, err := action.Execute(context.Background(), nil, &unit.Node{
				Input:  &unit.Input{Data: "2026-07-19T09:30:45+09:00"},
				Params: map[string]any{"format": test.format},
			})
			if err != nil {
				t.Fatalf("format date: %v", err)
			}
			if result.Data != test.want {
				t.Fatalf("unexpected %s result: %#v", test.format, result.Data)
			}
		})
	}
}

func TestFormatDateUnitRejectsInvalidInputAndFormat(t *testing.T) {
	action := NewFormatDateUnit()
	for _, test := range []struct {
		name    string
		node    *unit.Node
		message string
	}{
		{name: "missing input", node: &unit.Node{}, message: "missing date input"},
		{name: "invalid input", node: &unit.Node{Input: &unit.Input{Data: 42}}, message: "must be RFC 3339 text"},
		{name: "invalid format", node: &unit.Node{Input: &unit.Input{Data: "2026-07-19T09:30:00Z"}, Params: map[string]any{"format": "custom"}}, message: "unsupported format"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := action.Execute(context.Background(), nil, test.node)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}
