package scheduler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
	"github.com/ninenhan/go-workflow/worker"
)

type retryTestExecutor struct {
	calls atomic.Int32
}

func (e *retryTestExecutor) Type() executor.Type { return "retry_test" }

func (e *retryTestExecutor) Execute(context.Context, executor.ExecuteTask) (executor.ExecuteResult, error) {
	e.calls.Add(1)
	return executor.ExecuteResult{Status: executor.StatusSucceeded, Output: "once"}, nil
}

type dropFirstResponseTransport struct {
	next    http.RoundTripper
	dropped atomic.Bool
}

func (t *dropFirstResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if !t.dropped.Swap(true) {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil, errors.New("simulated lost response")
	}
	return resp, nil
}

func TestRemoteHTTPClientRetriesLostResponseWithoutDuplicateExecution(t *testing.T) {
	impl := &retryTestExecutor{}
	registry := executor.NewRegistry()
	if err := registry.Register(impl); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	workerService, err := worker.NewService(worker.Options{Enabled: true, Registry: registry})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	server := httptest.NewServer(workerService.Handler())
	defer server.Close()

	transport := &dropFirstResponseTransport{next: http.DefaultTransport}
	client := NewRemoteHTTPClient(&http.Client{Transport: transport})
	result, err := client.Execute(context.Background(), workerproto.WorkerDescriptor{Endpoint: server.URL}, executor.ExecuteTask{
		DispatchID: "dispatch-lost-response",
		RunID:      "run-1", NodeID: "node-1", ExecutorType: string(impl.Type()),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Output != "once" || impl.calls.Load() != 1 {
		t.Fatalf("unexpected result=%+v calls=%d", result, impl.calls.Load())
	}
}
