package scheduler

import (
	"context"
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
