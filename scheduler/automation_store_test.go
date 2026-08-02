package scheduler

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGormAutomationStoreLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:automation-store?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	store, err := NewGormAutomationStore(db)
	if err != nil {
		t.Fatalf("new automation store: %v", err)
	}
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	desired := AutomationSchedule{
		Key:          "schedule-a",
		WorkflowID:   "workflow",
		WorkflowName: "Workflow",
		VersionID:    "workflow:v1",
		TriggerID:    "morning",
		Fingerprint:  "fingerprint-v1",
		Config: AutomationScheduleConfig{
			Frequency: AutomationDaily,
			Time:      "09:00",
			Timezone:  "UTC",
		},
		Enabled:   true,
		NextRunAt: now,
	}
	if err := store.ReconcileSchedules(ctx, []AutomationSchedule{desired}); err != nil {
		t.Fatalf("reconcile schedule: %v", err)
	}
	claimed, err := store.ClaimDueSchedules(ctx, now, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim schedule: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ClaimToken == "" {
		t.Fatalf("claimed schedules = %#v", claimed)
	}
	claimedAgain, err := store.ClaimDueSchedules(ctx, now, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim schedule again: %v", err)
	}
	if len(claimedAgain) != 0 {
		t.Fatalf("leased schedule was claimed twice: %#v", claimedAgain)
	}
	next := now.Add(24 * time.Hour)
	if err := store.CompleteSchedule(ctx, desired.Key, claimed[0].ClaimToken, "run-1", now, next); err != nil {
		t.Fatalf("complete schedule: %v", err)
	}
	schedules, err := store.ListSchedules(ctx)
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	if len(schedules) != 1 || schedules[0].LastRunID != "run-1" ||
		!schedules[0].LastRunAt.Equal(now) || !schedules[0].NextRunAt.Equal(next) {
		t.Fatalf("completed schedule = %#v", schedules)
	}

	desired.NextRunAt = now.Add(48 * time.Hour)
	if err := store.ReconcileSchedules(ctx, []AutomationSchedule{desired}); err != nil {
		t.Fatalf("reconcile unchanged schedule: %v", err)
	}
	schedules, err = store.ListSchedules(ctx)
	if err != nil {
		t.Fatalf("list unchanged schedule: %v", err)
	}
	if !schedules[0].NextRunAt.Equal(next) {
		t.Fatalf("unchanged schedule reset next run: %s", schedules[0].NextRunAt)
	}

	desired.Fingerprint = "fingerprint-v2"
	if err := store.ReconcileSchedules(ctx, []AutomationSchedule{desired}); err != nil {
		t.Fatalf("reconcile changed schedule: %v", err)
	}
	schedules, err = store.ListSchedules(ctx)
	if err != nil {
		t.Fatalf("list changed schedule: %v", err)
	}
	if !schedules[0].NextRunAt.Equal(desired.NextRunAt) || schedules[0].LastRunID != "run-1" {
		t.Fatalf("changed schedule = %#v", schedules[0])
	}

	if err := store.ReconcileSchedules(ctx, nil); err != nil {
		t.Fatalf("disable stale schedule: %v", err)
	}
	schedules, err = store.ListSchedules(ctx)
	if err != nil {
		t.Fatalf("list disabled schedules: %v", err)
	}
	if len(schedules) != 0 {
		t.Fatalf("stale schedule remains enabled: %#v", schedules)
	}
}

func TestGormAutomationStoreReleasesFailedClaim(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:automation-failure?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	store, err := NewGormAutomationStore(db)
	if err != nil {
		t.Fatalf("new automation store: %v", err)
	}
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	schedule := AutomationSchedule{
		Key: "failure", WorkflowID: "workflow", WorkflowName: "Workflow",
		VersionID: "workflow:v1", TriggerID: "trigger", Fingerprint: "fingerprint",
		Config:  AutomationScheduleConfig{Frequency: AutomationInterval, IntervalMinutes: 15, Timezone: "UTC"},
		Enabled: true, NextRunAt: now,
	}
	if err := store.ReconcileSchedules(context.Background(), []AutomationSchedule{schedule}); err != nil {
		t.Fatalf("reconcile schedule: %v", err)
	}
	claimed, err := store.ClaimDueSchedules(context.Background(), now, 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim schedule = %#v, %v", claimed, err)
	}
	retryAt := now.Add(time.Minute)
	if err := store.FailSchedule(context.Background(), schedule.Key, claimed[0].ClaimToken, "temporary failure", retryAt); err != nil {
		t.Fatalf("fail schedule: %v", err)
	}
	schedules, err := store.ListSchedules(context.Background())
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	if schedules[0].LastError != "temporary failure" || !schedules[0].NextRunAt.Equal(retryAt) {
		t.Fatalf("failed schedule = %#v", schedules[0])
	}
}
