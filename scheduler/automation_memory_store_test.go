package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestMemoryAutomationStoreLifecycle(t *testing.T) {
	store := NewMemoryAutomationStore()
	now := time.Date(2026, 8, 23, 1, 0, 0, 0, time.UTC)
	schedule := AutomationSchedule{
		Key:          "memory-schedule",
		WorkflowID:   "workflow",
		WorkflowName: "Workflow",
		VersionID:    "workflow:v1",
		TriggerID:    "trigger",
		Fingerprint:  "fingerprint",
		Config: AutomationScheduleConfig{
			Frequency:       AutomationInterval,
			IntervalMinutes: 15,
			Timezone:        "UTC",
		},
		Enabled:   true,
		NextRunAt: now,
	}
	ctx := context.Background()
	if err := store.ReconcileSchedules(ctx, []AutomationSchedule{schedule}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDueSchedules(ctx, now, 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ClaimToken == "" {
		t.Fatalf("claimed = %#v, err = %v", claimed, err)
	}
	if repeated, err := store.ClaimDueSchedules(ctx, now, 1, time.Minute); err != nil || len(repeated) != 0 {
		t.Fatalf("repeated claim = %#v, err = %v", repeated, err)
	}
	next := now.Add(15 * time.Minute)
	if err := store.CompleteSchedule(ctx, schedule.Key, claimed[0].ClaimToken, "run-1", now, next); err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListSchedules(ctx)
	if err != nil || len(listed) != 1 || listed[0].LastRunID != "run-1" || !listed[0].NextRunAt.Equal(next) {
		t.Fatalf("listed = %#v, err = %v", listed, err)
	}
	if err := store.ReconcileSchedules(ctx, nil); err != nil {
		t.Fatal(err)
	}
	listed, err = store.ListSchedules(ctx)
	if err != nil || len(listed) != 0 {
		t.Fatalf("disabled list = %#v, err = %v", listed, err)
	}
}
