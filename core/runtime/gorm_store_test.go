package wfruntime

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGormStore_RunSnapshotEventLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	store, err := NewGormStore(db)
	if err != nil {
		t.Fatalf("new gorm store: %v", err)
	}

	now := time.Date(2026, 3, 12, 13, 0, 0, 0, time.UTC)
	run := &WorkflowRun{
		ID:                "run-1",
		WorkflowID:        "wf-1",
		WorkflowVersionID: "wf-1:v1",
		PlanID:            "plan-1",
		Status:            StatusRunning,
		CurrentNodes:      []string{"n1"},
		NodeRuns: map[string]*NodeRun{
			"n1": {
				NodeID:      "n1",
				Status:      StatusRunning,
				Attempt:     1,
				MaxAttempts: 2,
				StartedAt:   now,
				Metadata:    map[string]any{"worker_id": "worker-1"},
			},
		},
		Context: RunContext{
			Variables:   map[string]any{"n1": map[string]any{"hello": "world"}},
			NodeResults: map[string]any{},
		},
		CreatedAt: now,
		UpdatedAt: now,
		StartedAt: now,
	}

	if err := store.SaveRun(context.Background(), run); err != nil {
		t.Fatalf("save run: %v", err)
	}
	if err := store.SaveSnapshot(context.Background(), &RunSnapshot{
		RunID:    run.ID,
		Status:   run.Status,
		NodeRuns: cloneNodeRuns(run.NodeRuns),
		Context:  run.Context,
		At:       now,
	}); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	if err := store.AppendEvent(context.Background(), RunEvent{
		RunID:      run.ID,
		WorkflowID: run.WorkflowID,
		Type:       EventNodeRunning,
		NodeID:     "n1",
		Status:     StatusRunning,
		Time:       now,
		Payload:    map[string]any{"attempt": float64(1)},
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	run.Status = StatusSuccess
	run.CurrentNodes = nil
	run.NodeRuns["n1"].Status = StatusSuccess
	run.NodeRuns["n1"].FinishedAt = now.Add(time.Second)
	run.NodeRuns["n1"].Result = map[string]any{"ok": true}
	run.Context.NodeResults["n1"] = map[string]any{"ok": true}
	run.UpdatedAt = now.Add(time.Second)
	run.FinishedAt = now.Add(time.Second)
	if err := store.SaveRun(context.Background(), run); err != nil {
		t.Fatalf("save updated run: %v", err)
	}

	loaded, err := store.LoadRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("load run: %v", err)
	}
	if loaded.Status != StatusSuccess {
		t.Fatalf("unexpected run status: %s", loaded.Status)
	}
	if loaded.NodeRuns["n1"] == nil || loaded.NodeRuns["n1"].Status != StatusSuccess {
		t.Fatalf("unexpected node run: %+v", loaded.NodeRuns["n1"])
	}

	runs, err := store.ListRuns(context.Background())
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("unexpected run count: %d", len(runs))
	}

	snapshots, err := store.Snapshots(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("unexpected snapshot count: %d", len(snapshots))
	}

	events, err := store.Events(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("unexpected event count: %d", len(events))
	}
	if events[0].Type != EventNodeRunning {
		t.Fatalf("unexpected event type: %s", events[0].Type)
	}
}
