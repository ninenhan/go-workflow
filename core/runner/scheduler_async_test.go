package runner

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/planning"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
)

type fakeAsyncExecutor struct {
	pollCount atomic.Int32
}

func (e *fakeAsyncExecutor) Type() executor.Type { return executor.TypeRemote }

func (e *fakeAsyncExecutor) Execute(context.Context, executor.ExecuteTask) (executor.ExecuteResult, error) {
	return executor.ExecuteResult{
		Status:         executor.StatusAccepted,
		ExternalTaskID: "task-remote-1",
		Metadata:       map[string]any{"queue": "default"},
	}, nil
}

func (e *fakeAsyncExecutor) Poll(context.Context, executor.ExecuteTask, string) (executor.ExecuteResult, error) {
	n := e.pollCount.Add(1)
	if n < 2 {
		return executor.ExecuteResult{Status: executor.StatusRunning}, nil
	}
	return executor.ExecuteResult{
		Status: executor.StatusSucceeded,
		Output: map[string]any{"ok": true},
	}, nil
}

func (e *fakeAsyncExecutor) Cancel(context.Context, executor.ExecuteTask, string) error { return nil }

type captureResultReporter struct {
	mu      sync.Mutex
	results []executor.ExecuteResult
}

func (r *captureResultReporter) ReportResult(_ context.Context, _ executor.ExecuteTask, result executor.ExecuteResult) error {
	r.mu.Lock()
	r.results = append(r.results, result)
	r.mu.Unlock()
	return nil
}

func (r *captureResultReporter) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.results)
}

type captureHeartbeatReporter struct {
	count atomic.Int32
}

func (r *captureHeartbeatReporter) ReportHeartbeat(context.Context, Heartbeat) error {
	r.count.Add(1)
	return nil
}

func TestDefaultScheduler_Run_AsyncExecutor(t *testing.T) {
	reg := executor.NewRegistry()
	asyncExec := &fakeAsyncExecutor{}
	if err := reg.Register(asyncExec); err != nil {
		t.Fatalf("register async executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	resultReporter := &captureResultReporter{}
	heartbeatReporter := &captureHeartbeatReporter{}
	scheduler.ResultReporter = resultReporter
	scheduler.HeartbeatReporter = heartbeatReporter

	plan := &planning.ExecutionPlan{
		PlanID:            "plan-async",
		WorkflowID:        "wf-async",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"remote-1"},
		Dependencies: map[string][]string{
			"remote-1": []string{},
		},
		Nodes: map[string]planning.PlanNode{
			"remote-1": {
				ID:           "remote-1",
				ExecutorType: string(executor.TypeRemote),
				ExecutorRef:  "remote.queue",
				Input:        map[string]any{"task": "do"},
				Params: map[string]any{
					"async":              true,
					"poll_interval":      "20ms",
					"heartbeat_interval": "10ms",
				},
				Retry: planning.RetryPolicy{MaxAttempts: 1},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	run, err := scheduler.Run(ctx, plan, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected run status: %s", run.Status)
	}
	nr := run.NodeRuns["remote-1"]
	if nr == nil || nr.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected node state: %+v", nr)
	}
	if asyncExec.pollCount.Load() < 2 {
		t.Fatalf("expected poll count >= 2, got %d", asyncExec.pollCount.Load())
	}
	if resultReporter.Count() < 2 {
		t.Fatalf("expected at least 2 result reports, got %d", resultReporter.Count())
	}
	if heartbeatReporter.count.Load() < 1 {
		t.Fatalf("expected heartbeat reports, got %d", heartbeatReporter.count.Load())
	}
}
