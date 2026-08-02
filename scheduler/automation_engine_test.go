package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAutomationEngineRunsDueScheduleOnce(t *testing.T) {
	svc, runtimeStore, automationStore := newAutomationTestService(t, "automation-engine")
	ctx := context.Background()
	version := savePublishedAutomationWorkflow(t, svc, "scheduled", "scheduled:v1")
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	if err := svc.SyncAutomations(ctx, now); err != nil {
		t.Fatalf("sync automations: %v", err)
	}
	schedules, err := automationStore.ListSchedules(ctx)
	if err != nil || len(schedules) != 1 {
		t.Fatalf("schedules = %#v, %v", schedules, err)
	}
	dueAt := now.Add(15 * time.Minute)
	if !schedules[0].NextRunAt.Equal(dueAt) {
		t.Fatalf("next run = %s, want %s", schedules[0].NextRunAt, dueAt)
	}
	if err := svc.RunDueAutomations(ctx, dueAt); err != nil {
		t.Fatalf("run due automations: %v", err)
	}
	runID := automationRunID(schedules[0].Key, dueAt)
	waitForStoredRunStatus(t, runtimeStore, runID, wfruntime.StatusSuccess)
	if err := svc.RunDueAutomations(ctx, dueAt); err != nil {
		t.Fatalf("run same occurrence again: %v", err)
	}
	runs, err := runtimeStore.ListRuns(ctx)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("same occurrence created %d runs", len(runs))
	}
	run := runs[0]
	if run.WorkflowVersionID != version.ID || run.CredentialScope != "local-workspace" {
		t.Fatalf("automation run identity = %#v", run)
	}
	automationContext, ok := run.Context.Variables["_automation"].(map[string]any)
	if !ok || automationContext["trigger_id"] != "every-fifteen" {
		t.Fatalf("automation run context = %#v", run.Context.Variables)
	}
	statuses, err := svc.ListAutomations(ctx)
	if err != nil || len(statuses) != 1 {
		t.Fatalf("automation statuses = %#v, %v", statuses, err)
	}
	if statuses[0].LastRunID != runID || statuses[0].LastRunStatus != wfruntime.StatusSuccess ||
		!statuses[0].NextRunAt.Equal(dueAt.Add(15*time.Minute)) {
		t.Fatalf("automation status = %#v", statuses[0])
	}
}

func TestAutomationEngineCompletesRecoveredOccurrenceWithoutDuplicate(t *testing.T) {
	svc, runtimeStore, automationStore := newAutomationTestService(t, "automation-recovery")
	savePublishedAutomationWorkflow(t, svc, "recovered", "recovered:v1")
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	if err := svc.SyncAutomations(ctx, now); err != nil {
		t.Fatalf("sync automations: %v", err)
	}
	dueAt := now.Add(15 * time.Minute)
	claimed, err := automationStore.ClaimDueSchedules(ctx, dueAt, 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim schedule = %#v, %v", claimed, err)
	}
	runID := automationRunID(claimed[0].Key, claimed[0].NextRunAt)
	existing := wfruntime.NewWorkflowRun(runID, "recovered", "recovered:v1", "plan")
	existing.Status = wfruntime.StatusSuccess
	existing.FinishedAt = dueAt
	if err := runtimeStore.SaveRun(ctx, existing); err != nil {
		t.Fatalf("save recovered run: %v", err)
	}
	if err := svc.startClaimedAutomation(ctx, claimed[0], dueAt); err != nil {
		t.Fatalf("complete recovered occurrence: %v", err)
	}
	runs, err := runtimeStore.ListRuns(ctx)
	if err != nil || len(runs) != 1 {
		t.Fatalf("recovered runs = %#v, %v", runs, err)
	}
	schedules, err := automationStore.ListSchedules(ctx)
	if err != nil || len(schedules) != 1 || schedules[0].LastRunID != runID {
		t.Fatalf("recovered schedule = %#v, %v", schedules, err)
	}
}

func TestValidateVersionRejectsInvalidAutomation(t *testing.T) {
	svc, _, _ := newAutomationTestService(t, "automation-validation")
	_, err := svc.ValidateDefinition(context.Background(), &definition.WorkflowDefinition{
		ID:         "invalid-automation",
		EntryNodes: []string{"text"},
		Nodes: []definition.Node{{
			ID: "text", Name: "Text",
			Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
			Params:   map[string]any{"text": "hello"},
		}},
		Triggers: []definition.Trigger{{
			ID: "legacy", Type: definition.TriggerCron, Enabled: true,
			Config: map[string]any{"cron": "* * * * *", "timezone": "UTC"},
		}},
	})
	if err == nil {
		t.Fatal("expected invalid automation to fail validation")
	}
}

func TestAutomationHTTPAPIListsScheduleMetadataOnly(t *testing.T) {
	svc, _, _ := newAutomationTestService(t, "automation-http")
	savePublishedAutomationWorkflow(t, svc, "http-scheduled", "http-scheduled:v1")
	if err := svc.SyncAutomations(context.Background(), time.Now()); err != nil {
		t.Fatalf("sync automations: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/v1/automations")
	if err != nil {
		t.Fatalf("list automations: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("automation status = %d", response.StatusCode)
	}
	var payload struct {
		Automations []AutomationStatus `json:"automations"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode automations: %v", err)
	}
	if len(payload.Automations) != 1 ||
		payload.Automations[0].WorkflowID != "http-scheduled" ||
		payload.Automations[0].Config.Frequency != AutomationInterval {
		t.Fatalf("automation payload = %#v", payload.Automations)
	}
}

func newAutomationTestService(
	t *testing.T,
	databaseName string,
) (*Service, *wfruntime.GormStore, *GormAutomationStore) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+databaseName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	definitions, err := definition.NewGormRepository(db)
	if err != nil {
		t.Fatalf("new definition repository: %v", err)
	}
	runtimeStore, err := wfruntime.NewGormStore(db)
	if err != nil {
		t.Fatalf("new runtime store: %v", err)
	}
	automationStore, err := NewGormAutomationStore(db)
	if err != nil {
		t.Fatalf("new automation store: %v", err)
	}
	svc, err := NewService(Options{
		EnableEmbeddedWorker:   true,
		Definitions:            definitions,
		Store:                  runtimeStore,
		Automations:            automationStore,
		DefaultCredentialScope: "local-workspace",
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, runtimeStore, automationStore
}

func savePublishedAutomationWorkflow(
	t *testing.T,
	svc *Service,
	workflowID, versionID string,
) *definition.WorkflowVersion {
	t.Helper()
	ctx := context.Background()
	if err := svc.SaveWorkflow(ctx, &definition.Workflow{ID: workflowID, Name: "Scheduled Workflow"}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	version := &definition.WorkflowVersion{
		ID:         versionID,
		WorkflowID: workflowID,
		Version:    1,
		Status:     definition.VersionDraft,
		Definition: &definition.WorkflowDefinition{
			ID:         workflowID,
			Name:       "Scheduled Workflow",
			EntryNodes: []string{"text"},
			Nodes: []definition.Node{{
				ID: "text", Name: "Text",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
				Params:   map[string]any{"text": "automated"},
			}},
			Triggers: []definition.Trigger{{
				ID:      "every-fifteen",
				Type:    definition.TriggerCron,
				Enabled: true,
				Config: map[string]any{
					"frequency":        "interval",
					"interval_minutes": 15,
					"timezone":         "UTC",
				},
			}},
		},
	}
	if err := svc.SaveVersion(ctx, version); err != nil {
		t.Fatalf("save version: %v", err)
	}
	published, err := svc.PublishVersion(ctx, version.ID)
	if err != nil {
		t.Fatalf("publish version: %v", err)
	}
	return published
}

func waitForStoredRunStatus(
	t *testing.T,
	store wfruntime.Store,
	runID string,
	status wfruntime.Status,
) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		run, err := store.LoadRun(context.Background(), runID)
		if err == nil && run.Status == status {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not reach %s: %#v, %v", runID, status, run, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
