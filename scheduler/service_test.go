package scheduler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/core/executor"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/worker"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type serviceFakeAsyncExecutor struct {
	pollCount atomic.Int32
}

func (e *serviceFakeAsyncExecutor) Type() executor.Type { return executor.TypeRemote }

func (e *serviceFakeAsyncExecutor) Execute(context.Context, executor.ExecuteTask) (executor.ExecuteResult, error) {
	return executor.ExecuteResult{
		Status:         executor.StatusAccepted,
		ExternalTaskID: "async-1",
	}, nil
}

func (e *serviceFakeAsyncExecutor) Poll(context.Context, executor.ExecuteTask, string) (executor.ExecuteResult, error) {
	if e.pollCount.Add(1) < 4 {
		time.Sleep(15 * time.Millisecond)
		return executor.ExecuteResult{Status: executor.StatusRunning}, nil
	}
	return executor.ExecuteResult{
		Status: executor.StatusSucceeded,
		Output: map[string]any{"ok": true},
	}, nil
}

func (e *serviceFakeAsyncExecutor) Cancel(context.Context, executor.ExecuteTask, string) error {
	return nil
}

func TestNewServiceWithoutEmbeddedWorker(t *testing.T) {
	svc, err := NewService(Options{
		EnableEmbeddedWorker: false,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if svc.EmbeddedWorker() != nil {
		t.Fatalf("expected no embedded worker")
	}
	if svc.Store() == nil {
		t.Fatalf("expected store")
	}
}

func TestNewServiceWithEmbeddedWorker(t *testing.T) {
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if svc.EmbeddedWorker() == nil {
		t.Fatalf("expected embedded worker")
	}
	if !svc.EmbeddedWorker().Enabled() {
		t.Fatalf("expected embedded worker enabled")
	}
}

func TestServiceRunDefinition(t *testing.T) {
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	run, err := svc.RunDefinition(context.Background(), &definition.WorkflowDefinition{
		ID:         "wf-1",
		Name:       "unit-demo",
		EntryNodes: []string{"n1"},
		Nodes: []definition.Node{
			{
				ID:       "n1",
				Name:     "log",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
				Input:    "hello",
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("run definition: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected run status: %s", run.Status)
	}
	if node := run.NodeRuns["n1"]; node == nil || node.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected node status: %+v", node)
	}
}

func TestServiceShutdownCancelsBackgroundRuns(t *testing.T) {
	store := wfruntime.NewMemoryStore()
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                store,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	accepted, err := svc.StartDefinition(context.Background(), &definition.WorkflowDefinition{
		ID:         "wf-shutdown",
		Name:       "shutdown",
		EntryNodes: []string{"wait"},
		Nodes: []definition.Node{{
			ID:       "wait",
			Name:     "wait",
			Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TimeoutUnit"},
			Input:    "3000",
		}},
	}, nil)
	if err != nil {
		t.Fatalf("start definition: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		run, loadErr := store.LoadRun(context.Background(), accepted.ID)
		if loadErr == nil && run.Status == wfruntime.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not start: %#v, %v", run, loadErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := svc.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown service: %v", err)
	}
	run, err := store.LoadRun(context.Background(), accepted.ID)
	if err != nil {
		t.Fatalf("load cancelled run: %v", err)
	}
	if run.Status != wfruntime.StatusCancelled || len(run.CurrentNodes) != 0 {
		t.Fatalf("shutdown run = %#v", run)
	}
	if _, err := svc.StartDefinition(context.Background(), &definition.WorkflowDefinition{
		ID:         "after-shutdown",
		EntryNodes: []string{"text"},
		Nodes: []definition.Node{{
			ID:       "text",
			Name:     "text",
			Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
			Params:   map[string]any{"text": "not started"},
		}},
	}, nil); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("start after shutdown error = %v", err)
	}
}

func TestServiceRunDefinitionWithGormStore(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	store, err := wfruntime.NewGormStore(db)
	if err != nil {
		t.Fatalf("new gorm store: %v", err)
	}
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                store,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	run, err := svc.RunDefinition(context.Background(), &definition.WorkflowDefinition{
		ID:         "wf-gorm-1",
		Name:       "gorm-demo",
		EntryNodes: []string{"n1"},
		Nodes: []definition.Node{
			{
				ID:       "n1",
				Name:     "log",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
				Input:    "persist-me",
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("run definition: %v", err)
	}
	loaded, err := svc.LoadRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("load persisted run: %v", err)
	}
	if loaded.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected persisted status: %s", loaded.Status)
	}
	events, err := svc.RunEvents(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("load persisted events: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected persisted events")
	}
}

func TestServicePauseAndResumeRun(t *testing.T) {
	reg := executor.NewRegistry()
	asyncExec := &serviceFakeAsyncExecutor{}
	if err := reg.Register(asyncExec); err != nil {
		t.Fatalf("register async executor: %v", err)
	}
	workerSvc, err := worker.NewService(worker.Options{
		Enabled:  true,
		Registry: reg,
	})
	if err != nil {
		t.Fatalf("new worker service: %v", err)
	}
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		EmbeddedWorker:       workerSvc,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if err := svc.SaveWorkflow(context.Background(), &definition.Workflow{ID: "wf-resume-1", Name: "resume"}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := svc.SaveVersion(context.Background(), &definition.WorkflowVersion{
		ID:         "wf-resume-1:v1",
		WorkflowID: "wf-resume-1",
		Version:    1,
		Status:     definition.VersionPublished,
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-resume-1",
			Name:       "resume",
			EntryNodes: []string{"n1"},
			Nodes: []definition.Node{
				{
					ID:       "n1",
					Name:     "async",
					Executor: definition.ExecutorSpec{Type: string(executor.TypeRemote), Ref: "remote.async"},
					Params: map[string]any{
						"async":              true,
						"poll_interval":      "10ms",
						"heartbeat_interval": "5ms",
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("save version: %v", err)
	}

	runID := "run-resume-1"
	done := make(chan *wfruntime.WorkflowRun, 1)
	errCh := make(chan error, 1)
	go func() {
		run, err := svc.RunVersionByID(context.Background(), "wf-resume-1:v1", &wfruntime.WorkflowRun{
			ID:                runID,
			WorkflowID:        "wf-resume-1",
			WorkflowVersionID: "wf-resume-1:v1",
			PlanID:            "plan-resume-1",
			NodeRuns:          map[string]*wfruntime.NodeRun{},
			Context: wfruntime.RunContext{
				Variables:   map[string]any{},
				NodeResults: map[string]any{},
			},
		})
		if err != nil {
			errCh <- err
			return
		}
		done <- run
	}()

	time.Sleep(30 * time.Millisecond)
	if _, err := svc.PauseRun(context.Background(), runID); err != nil {
		t.Fatalf("pause run: %v", err)
	}

	var paused *wfruntime.WorkflowRun
	select {
	case err := <-errCh:
		t.Fatalf("run returned error: %v", err)
	case paused = <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for paused run")
	}
	if paused.Status != wfruntime.StatusPaused {
		t.Fatalf("unexpected paused status: %s", paused.Status)
	}

	resumed, err := svc.ResumeRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if resumed.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected resumed status: %s", resumed.Status)
	}
}

func TestServiceResumeInterruptedRun(t *testing.T) {
	store := wfruntime.NewMemoryStore()
	svc, err := NewService(Options{EnableEmbeddedWorker: true, Store: store})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	workflowID := "wf-interrupted-resume"
	versionID := workflowID + ":v1"
	if err := svc.SaveWorkflow(context.Background(), &definition.Workflow{ID: workflowID, Name: "resume interrupted"}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := svc.SaveVersion(context.Background(), &definition.WorkflowVersion{
		ID:         versionID,
		WorkflowID: workflowID,
		Version:    1,
		Status:     definition.VersionPublished,
		Definition: &definition.WorkflowDefinition{
			ID: workflowID, Name: "resume interrupted", EntryNodes: []string{"text"},
			Nodes: []definition.Node{{
				ID: "text", Name: "text", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
				Params: map[string]any{"text": "recovered"}, Retry: &definition.RetryPolicy{MaxAttempts: 3},
			}},
		},
	}); err != nil {
		t.Fatalf("save version: %v", err)
	}
	run := wfruntime.NewWorkflowRun("run-interrupted-resume", workflowID, versionID, "persisted-plan")
	run.Status = wfruntime.StatusRunning
	run.StartedAt = time.Now().Add(-time.Minute)
	run.CurrentNodes = []string{"text"}
	run.NodeRuns["text"] = &wfruntime.NodeRun{
		NodeID: "text", Status: wfruntime.StatusRunning, Attempt: 1, MaxAttempts: 3,
	}
	if err := store.SaveRun(context.Background(), run); err != nil {
		t.Fatalf("save interrupted run: %v", err)
	}

	resumed, err := svc.ResumeRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("resume interrupted run: %v", err)
	}
	if resumed.Status != wfruntime.StatusSuccess || resumed.NodeRuns["text"].Attempt != 2 {
		t.Fatalf("unexpected resumed run: status=%s node=%+v", resumed.Status, resumed.NodeRuns["text"])
	}
}

func TestServicePublishVersionRejectsRouteConflict(t *testing.T) {
	svc, err := NewService(Options{EnableEmbeddedWorker: true})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	createDefinition := func(id string) *definition.WorkflowDefinition {
		return &definition.WorkflowDefinition{
			ID: id, Name: id, EntryNodes: []string{"n1"},
			PublishConfig: &definition.PublishConfig{Enabled: true, Route: "/api/shared", Method: "POST"},
			Nodes: []definition.Node{{
				ID: "n1", Name: "log", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}, Input: "hello",
			}},
		}
	}

	for _, workflowID := range []string{"wf-route-a", "wf-route-b"} {
		if err := svc.SaveWorkflow(context.Background(), &definition.Workflow{ID: workflowID, Name: workflowID}); err != nil {
			t.Fatalf("save workflow %s: %v", workflowID, err)
		}
		version, createErr := svc.CreateVersion(context.Background(), workflowID, createDefinition(workflowID))
		if createErr != nil {
			t.Fatalf("create version %s: %v", workflowID, createErr)
		}
		if workflowID == "wf-route-a" {
			if _, publishErr := svc.PublishVersion(context.Background(), version.ID); publishErr != nil {
				t.Fatalf("publish first route: %v", publishErr)
			}
			continue
		}
		if _, publishErr := svc.PublishVersion(context.Background(), version.ID); publishErr == nil {
			t.Fatal("expected route conflict")
		}
	}
}

func TestServicePublishVersionRejectsUnavailableAuthentication(t *testing.T) {
	svc, err := NewService(Options{EnableEmbeddedWorker: true})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	const workflowID = "wf-auth-required"
	if err := svc.SaveWorkflow(context.Background(), &definition.Workflow{ID: workflowID, Name: workflowID}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	version, err := svc.CreateVersion(context.Background(), workflowID, &definition.WorkflowDefinition{
		ID: workflowID, Name: workflowID, EntryNodes: []string{"n1"},
		PublishConfig: &definition.PublishConfig{
			Enabled: true, Route: "/api/private", Method: "POST", AuthRequired: true,
		},
		Nodes: []definition.Node{{
			ID: "n1", Name: "log", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}, Input: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if _, err := svc.PublishVersion(context.Background(), version.ID); err == nil {
		t.Fatal("expected unavailable authentication to reject publication")
	}
}

func TestValidatePublishAdapterRequiresReturnResult(t *testing.T) {
	def := &definition.WorkflowDefinition{Nodes: []definition.Node{{
		ID: "n1", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
	}}}
	config := &definition.PublishConfig{Enabled: true, InputMode: "body", ResponseMode: "result"}
	if err := validatePublishAdapter(def, config); err == nil {
		t.Fatal("expected result mode without TerminalUnit to fail")
	}
	def.Nodes = append(def.Nodes, definition.Node{
		ID: "return", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TerminalUnit"},
	})
	if err := validatePublishAdapter(def, config); err != nil {
		t.Fatalf("validate result adapter: %v", err)
	}
}

func TestBuildHTTPRunMapsQueryVariables(t *testing.T) {
	version := &definition.WorkflowVersion{ID: "wf-query:v1", WorkflowID: "wf-query"}
	request := httptest.NewRequest(http.MethodGet, "/api/query?id=42&tag=one&tag=two", nil)
	run, err := buildHTTPRun(version, request, "query")
	if err != nil {
		t.Fatalf("build http run: %v", err)
	}
	if run.Context.Variables["id"] != "42" {
		t.Fatalf("id variable = %#v", run.Context.Variables["id"])
	}
	tags, ok := run.Context.Variables["tag"].([]string)
	if !ok || len(tags) != 2 || tags[0] != "one" || tags[1] != "two" {
		t.Fatalf("tag variable = %#v", run.Context.Variables["tag"])
	}
	requestVariable, ok := run.Context.Variables["request"].(map[string]any)
	if !ok || requestVariable["method"] != http.MethodGet {
		t.Fatalf("request variable = %#v", run.Context.Variables["request"])
	}
}

func TestServiceStartDefinitionIsIdempotentForClientRunID(t *testing.T) {
	store := wfruntime.NewMemoryStore()
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                store,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	definitionForText := func(text string) *definition.WorkflowDefinition {
		return &definition.WorkflowDefinition{
			ID:         "wf-idempotent-run",
			EntryNodes: []string{"text"},
			Nodes: []definition.Node{{
				ID:       "text",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
				Params:   map[string]any{"text": text},
			}},
		}
	}
	newRun := func(input string) *wfruntime.WorkflowRun {
		return &wfruntime.WorkflowRun{
			ID: "client-run-1",
			Context: wfruntime.RunContext{
				Variables: map[string]any{"input": input},
			},
		}
	}

	var requests sync.WaitGroup
	requests.Add(2)
	results := make(chan *wfruntime.WorkflowRun, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			defer requests.Done()
			result, startErr := svc.StartDefinition(context.Background(), definitionForText("hello"), newRun("same"))
			results <- result
			errs <- startErr
		}()
	}
	requests.Wait()
	close(results)
	close(errs)
	for startErr := range errs {
		if startErr != nil {
			t.Fatalf("start concurrent request: %v", startErr)
		}
	}
	responses := make([]*wfruntime.WorkflowRun, 0, 2)
	for result := range results {
		responses = append(responses, result)
	}
	first, duplicate := responses[0], responses[1]
	if duplicate.ID != first.ID || duplicate.RequestFingerprint != first.RequestFingerprint {
		t.Fatalf("duplicate request returned a different run: first=%#v duplicate=%#v", first, duplicate)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		stored, loadErr := store.LoadRun(context.Background(), first.ID)
		if loadErr == nil && stored.Status == wfruntime.StatusSuccess {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idempotent run did not complete: run=%#v err=%v", stored, loadErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	events, err := store.Events(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("load run events: %v", err)
	}
	started := 0
	for _, event := range events {
		if event.Type == wfruntime.EventRunStarted {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("run started %d times; events=%#v", started, events)
	}

	if _, err := svc.StartDefinition(context.Background(), definitionForText("changed"), newRun("same")); err == nil || !strings.Contains(err.Error(), "different workflow request") {
		t.Fatalf("definition conflict error = %v", err)
	}
	if _, err := svc.StartDefinition(context.Background(), definitionForText("hello"), newRun("changed")); err == nil || !strings.Contains(err.Error(), "different workflow request") {
		t.Fatalf("input conflict error = %v", err)
	}
}
