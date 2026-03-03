package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ninenhan/go-workflow"
	"github.com/ninenhan/go-workflow/fn"
)

type RunOptionsInput struct {
	RunID       string   `json:"run_id,omitempty"`
	Start       []string `json:"start,omitempty"`
	Concurrency int      `json:"concurrency,omitempty"`
	FailFast    *bool    `json:"fail_fast,omitempty"`
	AllowCycles bool     `json:"allow_cycles,omitempty"`
}

type RunRequest struct {
	Definition json.RawMessage  `json:"definition"`
	Options    *RunOptionsInput `json:"options,omitempty"`
}

type RunResponse struct {
	WorkflowID string `json:"workflow_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	Status     string `json:"status,omitempty"`
}

type APIServer struct {
	Engine   *workflow.Engine
	Store    workflow.StateStore
	Hub      *EventHub
	Logger   *slog.Logger
	mu       sync.RWMutex
	runIndex map[string]string // runID -> workflowID
}

func NewAPIServer(engine *workflow.Engine, store workflow.StateStore) *APIServer {
	if engine == nil {
		engine = workflow.NewEngine()
	}
	hub := NewEventHub(256)
	return &APIServer{
		Engine:   engine,
		Store:    store,
		Hub:      hub,
		Logger:   slog.Default(),
		runIndex: make(map[string]string),
	}
}

func (s *APIServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/runs", s.handleRun)
	mux.HandleFunc("/v1/runs/", s.handleRunSub)
	mux.HandleFunc("/v1/validate", s.handleValidate)
	return mux
}

func (s *APIServer) handleValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	doc, err := workflow.ParseComposerJSON(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := doc.ValidateBasic(workflow.LayoutValidatorFunc(workflow.ValidateLayoutBasic)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *APIServer) handleRun(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleRunStart(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *APIServer) handleRunSub(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "run id required")
		return
	}
	runID := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleRunState(w, r, runID)
		return
	}
	if len(parts) == 2 && parts[1] == "events" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleRunEvents(w, r, runID)
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *APIServer) handleRunStart(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.Definition) == 0 {
		writeError(w, http.StatusBadRequest, "missing definition")
		return
	}
	def, err := workflow.ParseWorkflowJSON(req.Definition)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := def.ValidateBasic(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	opts := &workflow.RunOptions{}
	if req.Options != nil {
		opts.RunID = req.Options.RunID
		opts.Start = req.Options.Start
		opts.Concurrency = req.Options.Concurrency
		opts.FailFast = req.Options.FailFast
		opts.AllowCycles = req.Options.AllowCycles
	}
	if opts.RunID == "" {
		if id, e := fn.GenerateShortID(); e == nil {
			opts.RunID = id
		} else {
			opts.RunID = fmt.Sprintf("run-%d", time.Now().UnixNano())
		}
	}
	opts.Store = s.Store
	opts.Sink = s.Hub

	if def.ID == "" {
		def.ID = "wf-" + opts.RunID
	}

	s.mu.Lock()
	s.runIndex[opts.RunID] = def.ID
	s.mu.Unlock()

	ctx := context.Background()
	go func() {
		_, runErr := s.Engine.Run(ctx, def, opts)
		if runErr != nil && s.Logger != nil {
			s.Logger.Error("workflow run failed", "run_id", opts.RunID, "err", runErr)
		}
		s.Hub.CloseRun(opts.RunID)
	}()

	writeJSON(w, http.StatusAccepted, RunResponse{
		WorkflowID: def.ID,
		RunID:      opts.RunID,
		Status:     "running",
	})
}

func (s *APIServer) handleRunState(w http.ResponseWriter, r *http.Request, runID string) {
	workflowID := s.workflowIDForRun(runID)
	if workflowID == "" {
		workflowID = r.URL.Query().Get("workflow_id")
	}
	if workflowID == "" {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if s.Store == nil {
		writeError(w, http.StatusNotImplemented, "state store not configured")
		return
	}
	state, err := s.Store.Load(r.Context(), workflowID, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *APIServer) handleRunEvents(w http.ResponseWriter, r *http.Request, runID string) {
	if s.Hub == nil {
		writeError(w, http.StatusNotImplemented, "event hub not configured")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	events, cancel := s.Hub.Subscribe(runID)
	defer cancel()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			payload, _ := json.Marshal(ev)
			writeSSE(w, string(ev.Type), payload)
			flusher.Flush()
		}
	}
}

func (s *APIServer) workflowIDForRun(runID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runIndex[runID]
}

func writeSSE(w http.ResponseWriter, event string, data []byte) {
	if event != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", string(data))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, errors.New("empty body")
	}
	defer r.Body.Close()
	return ioReadAllLimit(r.Body, 4<<20)
}

func ioReadAllLimit(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 4 << 20
	}
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 4096)
	var total int64
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			total += int64(n)
			if total > limit {
				return nil, errors.New("body too large")
			}
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
	}
	return buf, nil
}
