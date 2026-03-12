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
	mux.HandleFunc(workerproto.DefaultExecutePath, s.handleExecute)
	mux.HandleFunc(workerproto.DefaultPollPath, s.handlePoll)
	mux.HandleFunc(workerproto.DefaultCancelPath, s.handleCancel)
	return mux
}

func (s *Service) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req workerproto.ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	result, err := s.executeTask(r.Context(), req.Task)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, workerproto.ExecuteResponse{Result: result})
}

func (s *Service) handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req workerproto.PollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	result, err := s.pollTask(r.Context(), req.Task, req.ExternalTaskID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, workerproto.PollResponse{Result: result})
}

func (s *Service) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req workerproto.CancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if err := s.cancelTask(r.Context(), req.Task, req.ExternalTaskID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, workerproto.CancelResponse{Cancelled: true})
}

func (s *Service) executeTask(ctx context.Context, task executor.ExecuteTask) (executor.ExecuteResult, error) {
	if s == nil || !s.enabled {
		return executor.ExecuteResult{}, executor.ErrNotImplemented
	}
	execImpl, ok := s.registry.Get(executor.Type(task.ExecutorType))
	if !ok {
		return executor.ExecuteResult{}, httpError("executor not found")
	}
	return execImpl.Execute(ctx, task)
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
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type httpErr string

func (e httpErr) Error() string { return string(e) }

func httpError(msg string) error { return httpErr(msg) }
