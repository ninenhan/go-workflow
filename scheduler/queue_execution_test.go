package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/core/executor"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/worker"
)

func TestServiceRunDefinition_QueueExecutor(t *testing.T) {
	broker := executor.NewInMemoryQueueBroker()

	workerSvc, err := worker.NewService(worker.Options{
		Enabled:          true,
		RegisterBuiltins: true,
	})
	if err != nil {
		t.Fatalf("new worker service: %v", err)
	}
	if err := workerSvc.RegisterExecutor(executor.NewQueueExecutor(broker)); err != nil {
		t.Fatalf("register queue executor: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- workerSvc.ConsumeQueue(ctx, broker, "default", 10*time.Millisecond)
	}()

	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		EmbeddedWorker:       workerSvc,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new scheduler service: %v", err)
	}

	run, err := svc.RunDefinition(context.Background(), &definition.WorkflowDefinition{
		ID:         "wf-queue-1",
		Name:       "queue-demo",
		EntryNodes: []string{"n1"},
		Nodes: []definition.Node{
			{
				ID:       "n1",
				Name:     "queued-log",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeQueue, Ref: "default"},
				Input:    "hello-queue",
				Params: map[string]any{
					"async":                true,
					"poll_interval":        "10ms",
					"target_executor_type": string(executor.TypeUnit),
					"target_executor_ref":  "LogUnit",
				},
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
		t.Fatalf("unexpected node run: %+v", node)
	}

	cancel()
	if err := <-done; err != nil && err != context.Canceled {
		t.Fatalf("unexpected consumer error: %v", err)
	}
}
