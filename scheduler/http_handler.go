package scheduler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/core/planning"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

const CredentialScopeHeader = "X-Workflow-Credential-Scope"

const maxCredentialPayloadBytes = credential.MaxBatchSize*credential.MaxValueBytes + 64<<10
const maxRunPayloadBytes int64 = 16 << 20

type RunRequest struct {
	Definition *definition.WorkflowDefinition `json:"definition,omitempty"`
	Version    *definition.WorkflowVersion    `json:"version,omitempty"`
	VersionID  string                         `json:"version_id,omitempty"`
	Run        *wfruntime.WorkflowRun         `json:"run,omitempty"`
}

type ValidateResponse struct {
	OK   bool                    `json:"ok"`
	Plan *planning.ExecutionPlan `json:"plan,omitempty"`
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
	mux.HandleFunc("/v1/validate", h.handleValidate)
	mux.HandleFunc("/v1/workflows", h.handleWorkflows)
	mux.HandleFunc("/v1/workflows/", h.handleWorkflowResource)
	mux.HandleFunc("/v1/workflow-versions/", h.handleWorkflowVersionResource)
	mux.HandleFunc("/v1/credentials", h.handleCredentials)
	mux.HandleFunc("/v1/credentials/", h.handleCredentialResource)
	mux.HandleFunc("/v1/automations", h.handleAutomations)
	mux.HandleFunc("/v1/workers", h.handleWorkers)
	mux.HandleFunc(workerproto.DefaultRegisterPath, h.handleRegister)
	mux.HandleFunc(workerproto.DefaultHeartbeatPath, h.handleHeartbeat)
	mux.HandleFunc("/", h.handlePublishedOrTrigger)
	return mux
}

func (h *HTTPHandler) handleAutomations(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Service == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "scheduler service not configured"})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	automations, err := h.Service.ListAutomations(r.Context())
	if err != nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": err.Error()})
		return
	}
	payload := map[string]any{"automations": automations}
	if schedulerError := h.Service.AutomationError(); schedulerError != "" {
		payload["scheduler_error"] = schedulerError
	}
	writeJSON(w, http.StatusOK, payload)
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

	r.Body = http.MaxBytesReader(w, r.Body, maxRunPayloadBytes)
	var req RunRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "run payload is too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	var err error
	req.Run, err = h.runWithCredentialScope(req.Run, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	var (
		run *wfruntime.WorkflowRun
	)
	wait := !strings.EqualFold(r.URL.Query().Get("wait"), "false")
	idempotentWait := wait && req.Run != nil && strings.TrimSpace(req.Run.ID) != ""
	startAndWait := func(start func() (*wfruntime.WorkflowRun, error)) (*wfruntime.WorkflowRun, error) {
		accepted, startErr := start()
		if startErr != nil {
			return nil, startErr
		}
		return h.Service.waitForRun(r.Context(), accepted.ID)
	}
	switch {
	case req.Version != nil:
		if idempotentWait {
			run, err = startAndWait(func() (*wfruntime.WorkflowRun, error) {
				return h.Service.StartVersion(r.Context(), req.Version, req.Run)
			})
		} else if wait {
			run, err = h.Service.RunVersion(r.Context(), req.Version, req.Run)
		} else {
			run, err = h.Service.StartVersion(r.Context(), req.Version, req.Run)
		}
	case req.VersionID != "":
		if idempotentWait {
			run, err = startAndWait(func() (*wfruntime.WorkflowRun, error) {
				return h.Service.StartVersionByID(r.Context(), req.VersionID, req.Run)
			})
		} else if wait {
			run, err = h.Service.RunVersionByID(r.Context(), req.VersionID, req.Run)
		} else {
			run, err = h.Service.StartVersionByID(r.Context(), req.VersionID, req.Run)
		}
	case req.Definition != nil:
		if idempotentWait {
			run, err = startAndWait(func() (*wfruntime.WorkflowRun, error) {
				return h.Service.StartDefinition(r.Context(), req.Definition, req.Run)
			})
		} else if wait {
			run, err = h.Service.RunDefinition(r.Context(), req.Definition, req.Run)
		} else {
			run, err = h.Service.StartDefinition(r.Context(), req.Definition, req.Run)
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "definition, version, or version_id is required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	status := http.StatusOK
	if !wait {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]any{"run": run})
}

func (h *HTTPHandler) handleCredentials(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Service == nil || h.Service.CredentialStore() == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "credential store not configured"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		scope, err := h.credentialScope(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		credentials, err := h.Service.CredentialStore().ListCredentials(r.Context(), scope)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"credentials": credentials})
	case http.MethodPut:
		scope, err := h.credentialScope(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		var request struct {
			Credentials map[string]string `json:"credentials"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxCredentialPayloadBytes)
		if err := decodeCredentialJSON(r.Body, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid credential payload"})
			return
		}
		credentials, err := h.Service.CredentialStore().PutCredentials(r.Context(), scope, request.Credentials)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"credentials": credentials})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (h *HTTPHandler) handleCredentialResource(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Service == nil || h.Service.CredentialStore() == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "credential store not configured"})
		return
	}
	scope, err := h.credentialScope(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/credentials/"), "/")
	name, err = credential.NormalizeName(name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	switch r.Method {
	case http.MethodPut:
		var request struct {
			Value string `json:"value"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, credential.MaxValueBytes+1024)
		if err := decodeCredentialJSON(r.Body, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid credential payload"})
			return
		}
		metadata, err := h.Service.CredentialStore().PutCredential(r.Context(), scope, name, request.Value)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"credential": metadata})
	case http.MethodDelete:
		if err := h.Service.CredentialStore().DeleteCredential(r.Context(), scope, name); err != nil {
			if errors.Is(err, credential.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func decodeCredentialJSON(body io.Reader, target any) error {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("credential payload must contain one JSON document")
		}
		return err
	}
	return nil
}

func (h *HTTPHandler) credentialScope(r *http.Request) (string, error) {
	if h == nil || h.Service == nil {
		return "", errors.New("scheduler service is not configured")
	}
	if r == nil {
		return "", errors.New("request is required")
	}
	requested := strings.TrimSpace(r.Header.Get(CredentialScopeHeader))
	if fixed := h.Service.DefaultCredentialScope(); fixed != "" {
		if requested != "" && requested != fixed {
			return "", errors.New("credential scope does not match this server")
		}
		return fixed, nil
	}
	return credential.NormalizeScope(requested)
}

func (h *HTTPHandler) runWithCredentialScope(run *wfruntime.WorkflowRun, r *http.Request) (*wfruntime.WorkflowRun, error) {
	if r == nil {
		return run, nil
	}
	rawScope := strings.TrimSpace(r.Header.Get(CredentialScopeHeader))
	if rawScope == "" && h.Service.DefaultCredentialScope() == "" {
		return run, nil
	}
	scope, err := h.credentialScope(r)
	if err != nil {
		return nil, err
	}
	if run == nil {
		run = &wfruntime.WorkflowRun{}
	}
	if run.CredentialScope != "" && run.CredentialScope != scope {
		return nil, errors.New("credential scope conflicts with run payload")
	}
	run.CredentialScope = scope
	return run, nil
}

func (h *HTTPHandler) handleValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
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
		plan *planning.ExecutionPlan
		err  error
	)
	switch {
	case req.Version != nil:
		plan, err = h.Service.ValidateVersion(r.Context(), req.Version)
	case req.VersionID != "":
		var version *definition.WorkflowVersion
		version, err = h.Service.GetVersion(r.Context(), req.VersionID)
		if err == nil {
			plan, err = h.Service.ValidateVersion(r.Context(), version)
		}
	case req.Definition != nil:
		plan, err = h.Service.ValidateDefinition(r.Context(), req.Definition)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "definition, version, or version_id is required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ValidateResponse{
		OK:   true,
		Plan: plan,
	})
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
		persisted, err := h.Service.GetWorkflow(r.Context(), workflow.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workflow": persisted})
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
	if len(parts) == 2 && parts[1] == "invoke" {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		var request struct {
			Input map[string]any `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		version, err := h.Service.GetActiveVersion(r.Context(), workflowID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		publishedRequest, err := buildPublishedInvokeRequest(r.Context(), version, request.Input)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		run, err := h.Service.RunPublishedVersion(r.Context(), version, publishedRequest)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		payload, err := publishedWorkflowPayload(version.Definition, run)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, payload)
		return
	}
	if len(parts) == 2 && parts[1] == "contract" {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		version, err := h.Service.GetActiveVersion(r.Context(), workflowID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		contract, err := buildPublishedAPIContract(version)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		contract.URL = publishedAPIURL(r, contract.Route)
		writeJSON(w, http.StatusOK, map[string]any{"contract": contract})
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
		var err error
		switch {
		case req.Version != nil:
			version = req.Version
			version.WorkflowID = workflowID
			if version.Definition != nil && version.Definition.ID == "" {
				version.Definition.ID = workflowID
			}
			err = h.Service.SaveVersion(r.Context(), version)
		case req.Definition != nil:
			version, err = h.Service.CreateVersion(r.Context(), workflowID, req.Definition)
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "version or definition is required"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": version})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func publishedAPIURL(r *http.Request, route string) string {
	if r == nil {
		return ""
	}
	host := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return ""
	}
	scheme := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + host + route
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
		var err error
		req.Run, err = h.runWithCredentialScope(req.Run, r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
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
			run, err := h.Service.RunPublishedVersion(r.Context(), version, r)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			payload, err := publishedWorkflowPayload(version.Definition, run)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, payload)
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
