package scheduler

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/core/executor"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/core/workerproto"
	"github.com/ninenhan/go-workflow/worker"
)

func TestServiceRunVersionPullWorker(t *testing.T) {
	registry := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	if err := local.Register("echo", func(_ context.Context, task executor.Request) (executor.Result, error) {
		return executor.Result{Status: executor.StatusSucceeded, Output: task.Input}, nil
	}); err != nil {
		t.Fatalf("register local function: %v", err)
	}
	if err := registry.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}
	workerService, err := worker.NewService(worker.Options{Enabled: true, Registry: registry})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}

	service, err := NewService(Options{
		EnableEmbeddedWorker: false,
		DispatchMode:         DispatchRemoteOnly,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(service).Handler())
	defer server.Close()

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- workerService.MaintainPull(workerCtx, worker.PullOptions{
			SchedulerEndpoint: server.URL,
			Descriptor: workerproto.WorkerDescriptor{
				ID:                    "pull-worker-1",
				MaxConcurrent:         1,
				SupportedExecutorRefs: []string{"echo"},
			},
			RetryInterval: 10 * time.Millisecond,
		})
	}()
	waitForWorker(t, service, "pull-worker-1")

	version := &definition.WorkflowVersion{
		ID: "pull-workflow:v1", WorkflowID: "pull-workflow", Version: 1,
		Definition: &definition.WorkflowDefinition{
			ID: "pull-workflow", Name: "pull workflow", EntryNodes: []string{"echo"},
			Nodes: []definition.Node{{
				ID: "echo", Name: "echo",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeLocalGo, Ref: "echo"},
				Input:    map[string]any{"message": "hello"},
				Params:   map[string]any{"fn": "echo"},
			}},
		},
	}
	ctx, cancelRun := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRun()
	run, err := service.RunVersion(ctx, version, nil)
	if err != nil {
		t.Fatalf("run workflow: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess || run.NodeRuns["echo"].DispatchID == "" {
		t.Fatalf("unexpected run: status=%s node=%+v", run.Status, run.NodeRuns["echo"])
	}

	cancelWorker()
	if err := <-workerDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected worker shutdown: %v", err)
	}
}

func waitForWorker(t *testing.T, service *Service, workerID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		workers, err := service.ListWorkers(context.Background())
		if err != nil {
			t.Fatalf("list workers: %v", err)
		}
		for _, descriptor := range workers {
			if descriptor.ID == workerID {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("worker %s was not registered", workerID)
}
