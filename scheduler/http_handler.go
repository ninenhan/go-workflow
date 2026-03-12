package scheduler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

type RunRequest struct {
	Definition *definition.WorkflowDefinition `json:"definition,omitempty"`
	Version    *definition.WorkflowVersion    `json:"version,omitempty"`
	VersionID  string                         `json:"version_id,omitempty"`
	Run        *wfruntime.WorkflowRun         `json:"run,omitempty"`
}

type HTTPHandler struct {
	Service *Service
}

func NewHTTPHandler(service *Service) *HTTPHandler {
	return &HTTPHandler{Service: service}
}

func (h *HTTPHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/runs", h.handleRuns)
	mux.HandleFunc("/v1/runs/", h.handleRunResource)
	mux.HandleFunc("/v1/workflows", h.handleWorkflows)
	mux.HandleFunc("/v1/workflows/", h.handleWorkflowResource)
	mux.HandleFunc("/v1/workflow-versions/", h.handleWorkflowVersionResource)
	mux.HandleFunc("/v1/workers", h.handleWorkers)
	mux.HandleFunc(workerproto.DefaultRegisterPath, h.handleRegister)
	mux.HandleFunc(workerproto.DefaultHeartbeatPath, h.handleHeartbeat)
	mux.HandleFunc("/", h.handlePublishedOrTrigger)
	return mux
}

func (h *HTTPHandler) handleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handleRunCreate(w, r)
	case http.MethodGet:
		if h == nil || h.Service == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "scheduler service not configured"})
			return
		}
		runs, err := h.Service.ListRuns(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (h *HTTPHandler) handleRunCreate(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Service == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "scheduler service not configured"})
		return
	}

	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}

	var (
		run *wfruntime.WorkflowRun
		err error
	)
	switch {
	case req.Version != nil:
		run, err = h.Service.RunVersion(r.Context(), req.Version, req.Run)
	case req.VersionID != "":
		run, err = h.Service.RunVersionByID(r.Context(), req.VersionID, req.Run)
	case req.Definition != nil:
		run, err = h.Service.RunDefinition(r.Context(), req.Definition, req.Run)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "definition, version, or version_id is required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (h *HTTPHandler) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Service == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "scheduler service not configured"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		workflows, err := h.Service.ListWorkflows(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workflows": workflows})
	case http.MethodPost:
		var workflow definition.Workflow
		if err := json.NewDecoder(r.Body).Decode(&workflow); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		if err := h.Service.SaveWorkflow(r.Context(), &workflow); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workflow": workflow})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (h *HTTPHandler) handleWorkflowResource(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Service == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "scheduler service not configured"})
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/workflows/"), "/")
	if path == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "workflow id is required"})
		return
	}
	parts := strings.Split(path, "/")
	workflowID := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		workflow, err := h.Service.GetWorkflow(r.Context(), workflowID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workflow": workflow})
		return
	}
	if parts[1] != "versions" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "resource not found"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		versions, err := h.Service.ListVersions(r.Context(), workflowID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
	case http.MethodPost:
		var req struct {
			Version    *definition.WorkflowVersion    `json:"version,omitempty"`
			Definition *definition.WorkflowDefinition `json:"definition,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		var version *definition.WorkflowVersion
		switch {
		case req.Version != nil:
			version = req.Version
		case req.Definition != nil:
			version = &definition.WorkflowVersion{
				ID:         workflowID + ":draft",
				WorkflowID: workflowID,
				Version:    1,
				Status:     definition.VersionDraft,
				Definition: req.Definition,
			}
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "version or definition is required"})
			return
		}
		version.WorkflowID = workflowID
		if version.Definition != nil && version.Definition.ID == "" {
			version.Definition.ID = workflowID
		}
		if err := h.Service.SaveVersion(r.Context(), version); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": version})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (h *HTTPHandler) handleWorkflowVersionResource(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Service == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "scheduler service not configured"})
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/workflow-versions/"), "/")
	if path == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "version id is required"})
		return
	}
	parts := strings.Split(path, "/")
	versionID := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		version, err := h.Service.GetVersion(r.Context(), versionID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": version})
		return
	}
	switch parts[1] {
	case "publish":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		version, err := h.Service.PublishVersion(r.Context(), versionID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": version})
	case "runs":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		var req struct {
			Run *wfruntime.WorkflowRun `json:"run,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		run, err := h.Service.RunVersionByID(r.Context(), versionID, req.Run)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run": run})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "resource not found"})
	}
}

func (h *HTTPHandler) handleRunResource(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Service == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "scheduler service not configured"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	path = strings.Trim(path, "/")
	if path == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "run id is required"})
		return
	}

	parts := strings.Split(path, "/")
	runID := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			run, err := h.Service.LoadRun(r.Context(), runID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"run": run})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
		return
	}

	switch parts[1] {
	case "events":
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		events, err := h.Service.RunEvents(r.Context(), runID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events})
	case "snapshots":
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		snapshots, err := h.Service.RunSnapshots(r.Context(), runID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"snapshots": snapshots})
	case "pause":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		run, err := h.Service.PauseRun(r.Context(), runID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run": run, "requested": "pause"})
	case "resume":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		run, err := h.Service.ResumeRun(r.Context(), runID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run": run})
	case "cancel":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		run, err := h.Service.CancelRun(r.Context(), runID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run": run, "requested": "cancel"})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "resource not found"})
	}
}

func (h *HTTPHandler) handleWorkers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h == nil || h.Service == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "scheduler service not configured"})
		return
	}
	workers, err := h.Service.ListWorkers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	workers = filterWorkers(workers, r.URL.Query().Get("tenant"), r.URL.Query().Get("status"), parseLabelQuery(r.URL.Query().Get("labels")))
	writeJSON(w, http.StatusOK, map[string]any{"workers": workers})
}

func (h *HTTPHandler) handlePublishedOrTrigger(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "resource not found"})
		return
	}
	if h == nil || h.Service == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "scheduler service not configured"})
		return
	}
	workflows, err := h.Service.ListWorkflows(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	for _, workflow := range workflows {
		version, err := h.Service.GetActiveVersion(r.Context(), workflow.ID)
		if err != nil || version == nil || version.Definition == nil {
			continue
		}
		if publishMatches(version.Definition.PublishConfig, r) {
			run, err := h.Service.RunPublishedWorkflow(r.Context(), workflow.ID, r)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"run": run, "source": "publish"})
			return
		}
		if hasHTTPTrigger(version.Definition, r) {
			run, err := h.Service.RunHTTPTrigger(r.Context(), workflow.ID, r)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"run": run, "source": "trigger"})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "resource not found"})
}

func (h *HTTPHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req workerproto.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if h == nil || h.Service == nil || h.Service.WorkerRegistry() == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "worker registry not configured"})
		return
	}
	if err := h.Service.WorkerRegistry().Register(r.Context(), req.Worker); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, workerproto.RegisterResponse{Accepted: true})
}

func (h *HTTPHandler) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req workerproto.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if h == nil || h.Service == nil || h.Service.WorkerRegistry() == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "worker registry not configured"})
		return
	}
	if err := h.Service.WorkerRegistry().Heartbeat(r.Context(), req.WorkerID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type RegistryHTTPHandler struct {
	Workers WorkerRegistry
}

func NewRegistryHTTPHandler(workers WorkerRegistry) *RegistryHTTPHandler {
	return &RegistryHTTPHandler{Workers: workers}
}

func (h *RegistryHTTPHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(workerproto.DefaultRegisterPath, h.handleRegister)
	mux.HandleFunc(workerproto.DefaultHeartbeatPath, h.handleHeartbeat)
	return mux
}

func (h *RegistryHTTPHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req workerproto.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if h == nil || h.Workers == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "worker registry not configured"})
		return
	}
	if err := h.Workers.Register(r.Context(), req.Worker); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, workerproto.RegisterResponse{Accepted: true})
}

func (h *RegistryHTTPHandler) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req workerproto.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if h == nil || h.Workers == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "worker registry not configured"})
		return
	}
	if err := h.Workers.Heartbeat(r.Context(), req.WorkerID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func parseLabelQuery(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func filterWorkers(workers []workerproto.WorkerDescriptor, tenant, status string, labels map[string]string) []workerproto.WorkerDescriptor {
	if tenant == "" && status == "" && len(labels) == 0 {
		return workers
	}
	out := make([]workerproto.WorkerDescriptor, 0, len(workers))
	for _, worker := range workers {
		if tenant != "" && worker.Tenant != tenant {
			continue
		}
		if status != "" && string(worker.Status) != status {
			continue
		}
		if !matchesLabels(worker, labels) {
			continue
		}
		out = append(out, worker)
	}
	return out
}

func publishMatches(cfg *definition.PublishConfig, request *http.Request) bool {
	if cfg == nil || request == nil || !cfg.Enabled {
		return false
	}
	if cfg.Route != request.URL.Path {
		return false
	}
	return cfg.Method == "" || cfg.Method == request.Method
}
