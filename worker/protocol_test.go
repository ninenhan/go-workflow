package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

type protocolTestExecutor struct {
	calls atomic.Int32
	err   error
}

func (e *protocolTestExecutor) Type() executor.Type { return "protocol_test" }

func (e *protocolTestExecutor) Execute(context.Context, executor.ExecuteTask) (executor.ExecuteResult, error) {
	e.calls.Add(1)
	time.Sleep(10 * time.Millisecond)
	if e.err != nil {
		return executor.ExecuteResult{}, e.err
	}
	return executor.ExecuteResult{Status: executor.StatusSucceeded, Output: "ok"}, nil
}

func TestServiceExecuteTaskIsIdempotentByDispatchID(t *testing.T) {
	impl := &protocolTestExecutor{}
	registry := executor.NewRegistry()
	if err := registry.Register(impl); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	svc, err := NewService(Options{Enabled: true, Registry: registry})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	task := executor.ExecuteTask{DispatchID: "dispatch-1", RunID: "run-1", NodeID: "node-1", ExecutorType: string(impl.Type())}

	results := make(chan executor.ExecuteResult, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			result, executeErr := svc.executeTask(context.Background(), task)
			results <- result
			errs <- executeErr
		}()
	}
	for range 2 {
		if executeErr := <-errs; executeErr != nil {
			t.Fatalf("execute: %v", executeErr)
		}
		if result := <-results; result.Output != "ok" {
			t.Fatalf("unexpected result: %+v", result)
		}
	}
	if calls := impl.calls.Load(); calls != 1 {
		t.Fatalf("executor called %d times", calls)
	}
}

func TestWorkerHTTPSeparatesExecutionAndProtocolErrors(t *testing.T) {
	impl := &protocolTestExecutor{err: errors.New("provider unavailable")}
	registry := executor.NewRegistry()
	if err := registry.Register(impl); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	svc, err := NewService(Options{Enabled: true, Registry: registry})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	server := httptest.NewServer(svc.Handler())
	defer server.Close()

	resultResp := postExecute(t, server.URL, workerproto.ProtocolVersion, executor.ExecuteTask{
		DispatchID: "dispatch-error",
		RunID:      "run-1", NodeID: "node-1", ExecutorType: string(impl.Type()),
	})
	defer resultResp.Body.Close()
	if resultResp.StatusCode != http.StatusOK {
		t.Fatalf("execution error returned HTTP %d", resultResp.StatusCode)
	}
	var result workerproto.ExecuteResponse
	if err := json.NewDecoder(resultResp.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Result.Status != executor.StatusRetryable || result.Result.Error != "provider unavailable" {
		t.Fatalf("unexpected execution result: %+v", result.Result)
	}

	protocolResp := postExecute(t, server.URL, workerproto.ProtocolVersion, executor.ExecuteTask{
		DispatchID: "dispatch-missing",
		RunID:      "run-1", NodeID: "node-1", ExecutorType: "missing",
	})
	defer protocolResp.Body.Close()
	if protocolResp.StatusCode != http.StatusNotFound {
		t.Fatalf("protocol error returned HTTP %d", protocolResp.StatusCode)
	}
	var protocolError workerproto.ErrorResponse
	if err := json.NewDecoder(protocolResp.Body).Decode(&protocolError); err != nil {
		t.Fatalf("decode protocol error: %v", err)
	}
	if protocolError.Code != workerproto.ErrorExecutorNotFound {
		t.Fatalf("unexpected protocol error: %+v", protocolError)
	}

	versionResp := postExecute(t, server.URL, "999", executor.ExecuteTask{})
	defer versionResp.Body.Close()
	if versionResp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("unsupported version returned HTTP %d", versionResp.StatusCode)
	}

	legacyResp := postExecute(t, server.URL, "", executor.ExecuteTask{
		DispatchID: "dispatch-legacy",
		RunID:      "run-1", NodeID: "node-1", ExecutorType: string(impl.Type()),
	})
	defer legacyResp.Body.Close()
	if legacyResp.StatusCode != http.StatusOK {
		t.Fatalf("versionless compatibility request returned HTTP %d", legacyResp.StatusCode)
	}
}

func postExecute(t *testing.T, endpoint, version string, task executor.ExecuteTask) *http.Response {
	t.Helper()
	raw, err := json.Marshal(workerproto.ExecuteRequest{Task: task})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint+workerproto.DefaultExecutePath, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(workerproto.ProtocolHeader, version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post execute: %v", err)
	}
	return resp
}
