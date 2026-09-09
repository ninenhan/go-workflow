package scheduler

import (
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
)

func TestNextAutomationOccurrenceStructuredSchedules(t *testing.T) {
	t.Run("daily", func(t *testing.T) {
		after := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
		next, err := NextAutomationOccurrence(AutomationScheduleConfig{
			Frequency: AutomationDaily,
			Time:      "09:30",
			Timezone:  "Asia/Taipei",
		}, after)
		if err != nil {
			t.Fatalf("next daily occurrence: %v", err)
		}
		want := time.Date(2026, 7, 24, 1, 30, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Fatalf("next = %s, want %s", next, want)
		}
	})

	t.Run("weekdays skips weekend", func(t *testing.T) {
		after := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC) // Friday 10:00 in Taipei.
		next, err := NextAutomationOccurrence(AutomationScheduleConfig{
			Frequency: AutomationWeekdays,
			Time:      "09:00",
			Timezone:  "Asia/Taipei",
		}, after)
		if err != nil {
			t.Fatalf("next weekday occurrence: %v", err)
		}
		want := time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Fatalf("next = %s, want %s", next, want)
		}
	})

	t.Run("weekly selected days", func(t *testing.T) {
		after := time.Date(2026, 7, 27, 2, 0, 0, 0, time.UTC) // Monday 10:00 in Taipei.
		next, err := NextAutomationOccurrence(AutomationScheduleConfig{
			Frequency: AutomationWeekly,
			Time:      "08:00",
			Weekdays:  []int{1, 3},
			Timezone:  "Asia/Taipei",
		}, after)
		if err != nil {
			t.Fatalf("next weekly occurrence: %v", err)
		}
		want := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Fatalf("next = %s, want %s", next, want)
		}
	})

	t.Run("interval", func(t *testing.T) {
		after := time.Date(2026, 7, 24, 1, 2, 3, 0, time.UTC)
		next, err := NextAutomationOccurrence(AutomationScheduleConfig{
			Frequency:       AutomationInterval,
			IntervalMinutes: 15,
			Timezone:        "UTC",
		}, after)
		if err != nil {
			t.Fatalf("next interval occurrence: %v", err)
		}
		if want := after.Add(15 * time.Minute); !next.Equal(want) {
			t.Fatalf("next = %s, want %s", next, want)
		}
	})
}

func TestNextAutomationOccurrenceSkipsMissingDSTTime(t *testing.T) {
	after := time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)
	next, err := NextAutomationOccurrence(AutomationScheduleConfig{
		Frequency: AutomationDaily,
		Time:      "02:30",
		Timezone:  "America/New_York",
	}, after)
	if err != nil {
		t.Fatalf("next occurrence: %v", err)
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	local := next.In(location)
	if local.Day() != 9 || local.Hour() != 2 || local.Minute() != 30 {
		t.Fatalf("next local occurrence = %s", local)
	}
}

func TestParseAutomationScheduleRejectsFreeformAndInvalidValues(t *testing.T) {
	valid := definition.Trigger{
		ID:      "morning",
		Type:    definition.TriggerCron,
		Enabled: true,
		Config: map[string]any{
			"frequency": "weekly",
			"time":      "09:00",
			"weekdays":  []any{float64(1), float64(5), float64(1)},
			"timezone":  "Asia/Taipei",
		},
	}
	config, err := ParseAutomationSchedule(valid)
	if err != nil {
		t.Fatalf("parse valid schedule: %v", err)
	}
	if len(config.Weekdays) != 2 || config.Weekdays[0] != 1 || config.Weekdays[1] != 5 {
		t.Fatalf("normalized weekdays = %#v", config.Weekdays)
	}

	for name, trigger := range map[string]definition.Trigger{
		"cron expression": {
			ID: "legacy", Type: definition.TriggerCron, Enabled: true,
			Config: map[string]any{"cron": "* * * * *", "timezone": "UTC"},
		},
		"invalid timezone": {
			ID: "timezone", Type: definition.TriggerCron, Enabled: true,
			Config: map[string]any{"frequency": "daily", "time": "09:00", "timezone": "Nowhere/Invalid"},
		},
		"nonpositive interval": {
			ID: "interval", Type: definition.TriggerCron, Enabled: true,
			Config: map[string]any{"frequency": "interval", "interval_minutes": 0, "timezone": "UTC"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAutomationSchedule(trigger); err == nil {
				t.Fatal("expected invalid schedule to fail")
			}
		})
	}
}

func TestAutomationScheduleAcceptsNonWhitelistIntervals(t *testing.T) {
	for _, minutes := range []int{1, 10, 20, 45, 2900, 3600} {
		_, err := ParseAutomationSchedule(definition.Trigger{
			ID: "interval", Type: definition.TriggerCron, Enabled: true,
			Config: map[string]any{"frequency": "interval", "interval_minutes": minutes, "timezone": "UTC"},
		})
		if err != nil {
			t.Errorf("interval %d minutes: %v", minutes, err)
		}
	}
}

func TestAutomationRunIDIsDeterministicPerOccurrence(t *testing.T) {
	scheduledAt := time.Date(2026, 7, 24, 1, 2, 3, 0, time.UTC)
	first := automationRunID("schedule", scheduledAt)
	second := automationRunID("schedule", scheduledAt)
	if first != second {
		t.Fatalf("run ids differ: %q != %q", first, second)
	}
	if first == automationRunID("schedule", scheduledAt.Add(time.Second)) {
		t.Fatal("different occurrences reused a run id")
	}
}
