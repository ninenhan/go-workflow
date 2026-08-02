package units

import (
	"context"
	"strings"
	"testing"
	"time"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestDateUnitCurrentTimeUsesSelectedTimezone(t *testing.T) {
	previousNow := dateUnitNow
	dateUnitNow = func() time.Time {
		return time.Date(2026, time.July, 19, 9, 30, 45, 0, time.FixedZone("test", 8*60*60))
	}
	t.Cleanup(func() { dateUnitNow = previousNow })

	action := NewDateUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{
		Params: map[string]any{"mode": "current", "timezone": "UTC"},
	})
	if err != nil {
		t.Fatalf("execute current DateUnit: %v", err)
	}
	if result.Data != "2026-07-19T01:30:45Z" {
		t.Fatalf("unexpected current date: %#v", result.Data)
	}
}

func TestDateUnitSpecifiedTimeUsesSelectedTimezone(t *testing.T) {
	action := NewDateUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"mode": "specified", "date": "2026-07-19", "time": "09:30", "timezone": "Asia/Tokyo",
	}})
	if err != nil {
		t.Fatalf("execute specified DateUnit: %v", err)
	}
	if result.Data != "2026-07-19T09:30:00+09:00" {
		t.Fatalf("unexpected specified date: %#v", result.Data)
	}
}

func TestDateUnitRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		node    *unit.Node
		message string
	}{
		{name: "missing node", message: "missing node"},
		{name: "missing date", node: &unit.Node{Params: map[string]any{"mode": "specified", "time": "09:30"}}, message: "date is required"},
		{name: "missing time", node: &unit.Node{Params: map[string]any{"mode": "specified", "date": "2026-07-19"}}, message: "time is required"},
		{name: "impossible date", node: &unit.Node{Params: map[string]any{"mode": "specified", "date": "2026-02-30", "time": "09:30"}}, message: "date and time are invalid"},
		{name: "invalid time", node: &unit.Node{Params: map[string]any{"mode": "specified", "date": "2026-07-19", "time": "25:00"}}, message: "date and time are invalid"},
		{name: "unknown timezone", node: &unit.Node{Params: map[string]any{"timezone": "Mars/Olympus"}}, message: "load timezone"},
		{name: "empty timezone", node: &unit.Node{Params: map[string]any{"timezone": ""}}, message: "timezone must be non-empty"},
		{name: "unknown mode", node: &unit.Node{Params: map[string]any{"mode": "tomorrow"}}, message: "unsupported mode"},
	}
	action := NewDateUnit()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := action.Execute(context.Background(), nil, test.node)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}
