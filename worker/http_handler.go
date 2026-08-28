package worker

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/worker/protocol", s.handleProtocol)
	mux.HandleFunc(workerproto.DefaultExecutePath, s.handleExecute)
	mux.HandleFunc(workerproto.DefaultPollPath, s.handlePoll)
	mux.HandleFunc(workerproto.DefaultCancelPath, s.handleCancel)
	return mux
}

func (s *Service) handleExecute(w http.ResponseWriter, r *http.Request) {
	if !acceptProtocolVersion(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeProtocolError(w, http.StatusMethodNotAllowed, workerproto.ErrorInvalidRequest, "method not allowed", false)
		return
	}
	var req workerproto.ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, workerproto.ErrorInvalidJSON, "invalid json", false)
		return
	}
	result, err := s.executeTask(r.Context(), req.Task)
	if err != nil {
		if _, ok := err.(httpErr); ok {
			writeExecutionProtocolError(w, err)
			return
		}
		result = executionErrorResult(err)
	}
	writeJSON(w, http.StatusOK, workerproto.ExecuteResponse{Result: result})
}

func (s *Service) handlePoll(w http.ResponseWriter, r *http.Request) {
	if !acceptProtocolVersion(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeProtocolError(w, http.StatusMethodNotAllowed, workerproto.ErrorInvalidRequest, "method not allowed", false)
		return
	}
	var req workerproto.PollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, workerproto.ErrorInvalidJSON, "invalid json", false)
		return
	}
	result, err := s.pollTask(r.Context(), req.Task, req.ExternalTaskID)
	if err != nil {
		if _, ok := err.(httpErr); ok {
			writeExecutionProtocolError(w, err)
			return
		}
		result = executionErrorResult(err)
	}
	writeJSON(w, http.StatusOK, workerproto.PollResponse{Result: result})
}

func (s *Service) handleCancel(w http.ResponseWriter, r *http.Request) {
	if !acceptProtocolVersion(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeProtocolError(w, http.StatusMethodNotAllowed, workerproto.ErrorInvalidRequest, "method not allowed", false)
		return
	}
	var req workerproto.CancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, workerproto.ErrorInvalidJSON, "invalid json", false)
		return
	}
	if err := s.cancelTask(r.Context(), req.Task, req.ExternalTaskID); err != nil {
		code := workerproto.ErrorInternal
		status := http.StatusServiceUnavailable
		if _, ok := err.(httpErr); ok {
			code = workerproto.ErrorUnsupportedOperation
			status = http.StatusUnprocessableEntity
		}
		writeProtocolError(w, status, code, err.Error(), status >= 500)
		return
	}
	writeJSON(w, http.StatusOK, workerproto.CancelResponse{Cancelled: true})
}

func (s *Service) executeTask(ctx context.Context, task executor.ExecuteTask) (executor.ExecuteResult, error) {
	if s == nil || !s.enabled {
		return executor.ExecuteResult{}, executor.ErrNotImplemented
	}
	return s.executeOnce(ctx, task, func() (executor.ExecuteResult, error) {
		execImpl, ok := s.registry.Get(executor.Type(task.ExecutorType))
		if !ok {
			return executor.ExecuteResult{}, httpError("executor not found")
		}
		return execImpl.Execute(ctx, task)
	})
}

func (s *Service) pollTask(ctx context.Context, task executor.ExecuteTask, externalTaskID string) (executor.ExecuteResult, error) {
	if s == nil || !s.enabled {
		return executor.ExecuteResult{}, executor.ErrNotImplemented
	}
	execImpl, ok := s.registry.Get(executor.Type(task.ExecutorType))
	if !ok {
		return executor.ExecuteResult{}, httpError("executor not found")
	}
	asyncExec, ok := execImpl.(executor.AsyncExecutor)
	if !ok {
		return executor.ExecuteResult{}, httpError("executor does not support poll")
	}
	return asyncExec.Poll(ctx, task, externalTaskID)
}

func (s *Service) cancelTask(ctx context.Context, task executor.ExecuteTask, externalTaskID string) error {
	if s == nil || !s.enabled {
		return executor.ErrNotImplemented
	}
	execImpl, ok := s.registry.Get(executor.Type(task.ExecutorType))
	if !ok {
		return httpError("executor not found")
	}
	asyncExec, ok := execImpl.(executor.AsyncExecutor)
	if !ok {
		return httpError("executor does not support cancel")
	}
	return asyncExec.Cancel(ctx, task, externalTaskID)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(workerproto.ProtocolHeader, workerproto.ProtocolVersion)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Service) handleProtocol(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeProtocolError(w, http.StatusMethodNotAllowed, workerproto.ErrorInvalidRequest, "method not allowed", false)
		return
	}
	writeJSON(w, http.StatusOK, workerproto.CurrentProtocolInfo())
}

func acceptProtocolVersion(w http.ResponseWriter, r *http.Request) bool {
	version := r.Header.Get(workerproto.ProtocolHeader)
	if version == "" || version == workerproto.ProtocolVersion {
		return true
	}
	writeProtocolError(w, http.StatusUpgradeRequired, workerproto.ErrorProtocolVersion, "unsupported worker protocol version: "+version, false)
	return false
}

func writeProtocolError(w http.ResponseWriter, status int, code workerproto.ErrorCode, message string, retryable bool) {
	writeJSON(w, status, workerproto.ErrorResponse{Error: message, Code: code, Retryable: retryable})
}

func executionErrorResult(err error) executor.ExecuteResult {
	retryable, retryAfter, classified := executor.ClassifyFailure(err)
	if !classified {
		retryable = true
	}
	status := executor.StatusFailed
	if retryable {
		status = executor.StatusRetryable
	}
	return executor.ExecuteResult{Status: status, Error: err.Error(), RetryAfter: retryAfter}
}

func writeExecutionProtocolError(w http.ResponseWriter, err error) {
	code := workerproto.ErrorUnsupportedOperation
	status := http.StatusUnprocessableEntity
	if err.Error() == "executor not found" {
		code = workerproto.ErrorExecutorNotFound
		status = http.StatusNotFound
	}
	writeProtocolError(w, status, code, err.Error(), false)
}

type httpErr string

func (e httpErr) Error() string { return string(e) }

func httpError(msg string) error { return httpErr(msg) }
