package scheduler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/core/executor"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/core/workerproto"
	"github.com/ninenhan/go-workflow/worker"
)

func TestServiceRunVersion_RemoteWorker(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("echo", func(ctx context.Context, task executor.Request) (executor.Result, error) {
		return executor.Result{
			Status: executor.StatusSucceeded,
			Output: map[string]any{"value": task.Input},
		}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	workerSvc, err := worker.NewService(worker.Options{
		Enabled:  true,
		Registry: reg,
	})
	if err != nil {
		t.Fatalf("new worker service: %v", err)
	}
	workerHTTP := httptest.NewServer(workerSvc.Handler())
	defer workerHTTP.Close()

	workers := NewMemoryWorkerRegistry()
	if err := workers.Register(context.Background(), workerproto.WorkerDescriptor{
		ID:                     "worker-1",
		Endpoint:               workerHTTP.URL,
		Status:                 workerproto.StatusOnline,
		SupportedExecutorTypes: []string{string(executor.TypeLocalGo)},
		SupportedExecutorRefs:  []string{"echo"},
	}); err != nil {
		t.Fatalf("register worker: %v", err)
	}

	svc, err := NewService(Options{
		EnableEmbeddedWorker: false,
		WorkerRegistry:       workers,
		DispatchMode:         DispatchRemoteOnly,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new scheduler service: %v", err)
	}

	version := &definition.WorkflowVersion{
		ID:         "wf-1:v1",
		WorkflowID: "wf-1",
		Version:    1,
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-1",
			Name:       "remote-worker-demo",
			EntryNodes: []string{"a"},
			Nodes: []definition.Node{
				{
					ID:       "a",
					Name:     "echo",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeLocalGo, Ref: "echo"},
					Input:    "hello-remote",
					Params:   map[string]any{"fn": "echo"},
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	run, err := svc.RunVersion(ctx, version, nil)
	if err != nil {
		t.Fatalf("run version: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected run status: %s", run.Status)
	}
	node := run.NodeRuns["a"]
	if node == nil || node.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected node state: %+v", node)
	}
	result, ok := node.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected node result type: %T", node.Result)
	}
	if got := result["value"]; got != "hello-remote" {
		t.Fatalf("unexpected node result value: %#v", got)
	}

	list, err := workers.List(context.Background())
	if err != nil {
		t.Fatalf("list workers: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("unexpected worker count: %d", len(list))
	}
	if list[0].CurrentTasks != 0 {
		t.Fatalf("expected current tasks to be released, got %d", list[0].CurrentTasks)
	}
}
