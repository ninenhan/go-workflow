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
	result, err := client.Execute(context.Background(), workerproto.WorkerDescriptor{
		Endpoint:        server.URL,
		ProtocolVersion: workerproto.ProtocolVersion,
	}, executor.ExecuteTask{
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

func TestRemoteHTTPClientDoesNotRetryVersionlessWorker(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"result":{"status":"succeeded","output":"legacy"}}`)
	}))
	defer server.Close()

	transport := &dropFirstResponseTransport{next: http.DefaultTransport}
	client := NewRemoteHTTPClient(&http.Client{Transport: transport})
	_, err := client.Execute(context.Background(), workerproto.WorkerDescriptor{Endpoint: server.URL}, executor.ExecuteTask{
		DispatchID: "dispatch-legacy-worker",
		RunID:      "run-1",
		NodeID:     "node-1",
	})
	if err == nil {
		t.Fatalf("expected lost response without retry, got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("versionless worker was retried: calls=%d", got)
	}
}

func TestRemoteHTTPClientDoesNotReplayPollOrCancel(t *testing.T) {
	task := executor.ExecuteTask{DispatchID: "dispatch-async", RunID: "run-1", NodeID: "node-1"}
	tests := []struct {
		name     string
		response string
		call     func(*RemoteHTTPClient, workerproto.WorkerDescriptor) error
	}{
		{
			name:     "poll",
			response: `{"result":{"status":"running"}}`,
			call: func(client *RemoteHTTPClient, descriptor workerproto.WorkerDescriptor) error {
				_, err := client.Poll(context.Background(), descriptor, task, "external-1")
				return err
			},
		},
		{
			name:     "cancel",
			response: `{"cancelled":true}`,
			call: func(client *RemoteHTTPClient, descriptor workerproto.WorkerDescriptor) error {
				return client.Cancel(context.Background(), descriptor, task, "external-1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.response)
			}))
			defer server.Close()

			client := NewRemoteHTTPClient(&http.Client{Transport: &dropFirstResponseTransport{next: http.DefaultTransport}})
			err := tt.call(client, workerproto.WorkerDescriptor{
				Endpoint:        server.URL,
				ProtocolVersion: workerproto.ProtocolVersion,
			})
			if err == nil {
				t.Fatal("expected lost response without replay")
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("operation was replayed: calls=%d", got)
			}
		})
	}
}
