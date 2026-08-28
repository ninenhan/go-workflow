package worker

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

type pullCommandTestExecutor struct {
	cancelCalls atomic.Int32
	started     chan struct{}
	release     chan struct{}
}

func (e *pullCommandTestExecutor) Type() executor.Type { return "pull_command_test" }

func (e *pullCommandTestExecutor) Execute(context.Context, executor.ExecuteTask) (executor.ExecuteResult, error) {
	return executor.ExecuteResult{Status: executor.StatusAccepted, ExternalTaskID: "external-1"}, nil
}

func (e *pullCommandTestExecutor) Poll(context.Context, executor.ExecuteTask, string) (executor.ExecuteResult, error) {
	return executor.ExecuteResult{Status: executor.StatusRunning}, nil
}

func (e *pullCommandTestExecutor) Cancel(context.Context, executor.ExecuteTask, string) error {
	if e.cancelCalls.Add(1) == 1 {
		close(e.started)
	}
	<-e.release
	return nil
}

func TestServiceDeduplicatesConcurrentPullCommandDelivery(t *testing.T) {
	impl := &pullCommandTestExecutor{started: make(chan struct{}), release: make(chan struct{})}
	registry := executor.NewRegistry()
	if err := registry.Register(impl); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	svc, err := NewService(Options{Enabled: true, Registry: registry})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	command := &workerproto.Command{
		CommandID:      "command-1",
		Operation:      workerproto.OperationCancel,
		Task:           executor.ExecuteTask{DispatchID: "dispatch-1", ExecutorType: string(impl.Type())},
		ExternalTaskID: "external-1",
	}

	results := make(chan workerproto.CompleteRequest, 2)
	go func() { results <- svc.executeCommand(context.Background(), "worker-1", command) }()
	<-impl.started
	go func() { results <- svc.executeCommand(context.Background(), "worker-1", command) }()
	close(impl.release)

	for range 2 {
		if completion := <-results; !completion.Cancelled || completion.Error != "" {
			t.Fatalf("unexpected completion: %+v", completion)
		}
	}
	if got := impl.cancelCalls.Load(); got != 1 {
		t.Fatalf("cancel executed %d times", got)
	}

	svc.acknowledgePullCommand(command.CommandID)
	_ = svc.executeCommand(context.Background(), "worker-1", command)
	if got := impl.cancelCalls.Load(); got != 1 {
		t.Fatalf("acknowledged command executed again: %d", got)
	}
}

var _ executor.AsyncExecutor = (*pullCommandTestExecutor)(nil)
