package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/planning"
	"github.com/ninenhan/go-workflow/core/runner"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/core/workerproto"
	"github.com/ninenhan/go-workflow/worker"
)

type Options struct {
	Compiler             planning.Compiler
	Store                wfruntime.Store
	Definitions          definition.Repository
	RunController        runner.RunController
	EnableEmbeddedWorker bool
	EmbeddedWorker       *worker.Service
	WorkerRegistry       WorkerRegistry
	DispatchMode         DispatchMode
	ResultReporter       runner.ResultReporter
	HeartbeatReporter    runner.HeartbeatReporter
}

// Service is the orchestration entrypoint. It owns compilation, scheduling,
// runtime state, and optionally an embedded worker for single-binary deployments.
type Service struct {
	engine         *runner.Engine
	store          wfruntime.Store
	definitions    definition.Repository
	controller     runner.RunController
	workers        WorkerRegistry
	embeddedWorker *worker.Service
}

func NewService(opts Options) (*Service, error) {
	store := opts.Store
	if store == nil {
		store = wfruntime.NewMemoryStore()
	}
	workers := opts.WorkerRegistry
	if workers == nil {
		workers = NewMemoryWorkerRegistry()
	}
	defs := opts.Definitions
	if defs == nil {
		defs = definition.NewMemoryRepository()
	}
	controller := opts.RunController
	if controller == nil {
		controller = runner.NewMemoryRunController()
	}

	var reg *executor.Registry
	embeddedWorker := opts.EmbeddedWorker
	if opts.EnableEmbeddedWorker {
		if embeddedWorker == nil {
			var err error
			embeddedWorker, err = worker.NewService(worker.Options{
				Enabled:          true,
				RegisterBuiltins: true,
			})
			if err != nil {
				return nil, err
			}
		}
		reg = embeddedWorker.Registry()
	} else {
		reg = executor.NewRegistry()
	}

	scheduler := runner.NewDefaultScheduler(reg, store)
	scheduler.RunController = controller
	dispatcher := NewHybridDispatcher(reg, workers, nil)
	if opts.DispatchMode != "" {
		dispatcher.Mode = opts.DispatchMode
	} else if !opts.EnableEmbeddedWorker {
		dispatcher.Mode = DispatchRemoteOnly
	}
	scheduler.ExecutorDispatcher = dispatcher
	if opts.ResultReporter != nil {
		scheduler.ResultReporter = opts.ResultReporter
	}
	if opts.HeartbeatReporter != nil {
		scheduler.HeartbeatReporter = opts.HeartbeatReporter
	}

	engine := runner.NewEngine(opts.Compiler, scheduler)
	return &Service{
		engine:         engine,
		store:          store,
		definitions:    defs,
		controller:     controller,
		workers:        workers,
		embeddedWorker: embeddedWorker,
	}, nil
}

func (s *Service) RunVersion(ctx context.Context, version *definition.WorkflowVersion, run *wfruntime.WorkflowRun) (*wfruntime.WorkflowRun, error) {
	return s.engine.RunVersion(ctx, version, run)
}

func (s *Service) RunDefinition(ctx context.Context, def *definition.WorkflowDefinition, run *wfruntime.WorkflowRun) (*wfruntime.WorkflowRun, error) {
	if def == nil {
		return nil, errors.New("workflow definition is nil")
	}
	workflowID := def.ID
	if workflowID == "" {
		workflowID = "workflow"
	}
	version := &definition.WorkflowVersion{
		ID:         workflowID + ":latest",
		WorkflowID: workflowID,
		Version:    1,
		Status:     definition.VersionPublished,
		Definition: def,
		CreatedAt:  time.Now(),
	}
	return s.RunVersion(ctx, version, run)
}

func (s *Service) SaveWorkflow(ctx context.Context, workflow *definition.Workflow) error {
	if s == nil || s.definitions == nil {
		return errors.New("definition repository is not configured")
	}
	return s.definitions.SaveWorkflow(ctx, workflow)
}

func (s *Service) GetWorkflow(ctx context.Context, workflowID string) (*definition.Workflow, error) {
	if s == nil || s.definitions == nil {
		return nil, errors.New("definition repository is not configured")
	}
	return s.definitions.GetWorkflow(ctx, workflowID)
}

func (s *Service) ListWorkflows(ctx context.Context) ([]*definition.Workflow, error) {
	if s == nil || s.definitions == nil {
		return nil, errors.New("definition repository is not configured")
	}
	return s.definitions.ListWorkflows(ctx)
}

func (s *Service) SaveVersion(ctx context.Context, version *definition.WorkflowVersion) error {
	if s == nil || s.definitions == nil {
		return errors.New("definition repository is not configured")
	}
	return s.definitions.SaveVersion(ctx, version)
}

func (s *Service) GetVersion(ctx context.Context, versionID string) (*definition.WorkflowVersion, error) {
	if s == nil || s.definitions == nil {
		return nil, errors.New("definition repository is not configured")
	}
	return s.definitions.GetVersion(ctx, versionID)
}

func (s *Service) GetActiveVersion(ctx context.Context, workflowID string) (*definition.WorkflowVersion, error) {
	if s == nil || s.definitions == nil {
		return nil, errors.New("definition repository is not configured")
	}
	return s.definitions.GetActiveVersion(ctx, workflowID)
}

func (s *Service) ListVersions(ctx context.Context, workflowID string) ([]*definition.WorkflowVersion, error) {
	if s == nil || s.definitions == nil {
		return nil, errors.New("definition repository is not configured")
	}
	return s.definitions.ListVersions(ctx, workflowID)
}

func (s *Service) PublishVersion(ctx context.Context, versionID string) (*definition.WorkflowVersion, error) {
	if s == nil || s.definitions == nil {
		return nil, errors.New("definition repository is not configured")
	}
	return s.definitions.PublishVersion(ctx, versionID)
}

func (s *Service) RunVersionByID(ctx context.Context, versionID string, run *wfruntime.WorkflowRun) (*wfruntime.WorkflowRun, error) {
	version, err := s.GetVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}
	return s.RunVersion(ctx, version, run)
}

func (s *Service) PauseRun(ctx context.Context, runID string) (*wfruntime.WorkflowRun, error) {
	if s == nil || s.controller == nil {
		return nil, errors.New("run controller is not configured")
	}
	run, err := s.LoadRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if wfruntime.IsTerminal(run.Status) {
		return nil, errors.New("run is already terminal")
	}
	s.controller.Set(runID, runner.RunCommandPause)
	return run, nil
}

func (s *Service) CancelRun(ctx context.Context, runID string) (*wfruntime.WorkflowRun, error) {
	if s == nil || s.controller == nil {
		return nil, errors.New("run controller is not configured")
	}
	run, err := s.LoadRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if wfruntime.IsTerminal(run.Status) {
		return nil, errors.New("run is already terminal")
	}
	s.controller.Set(runID, runner.RunCommandCancel)
	return run, nil
}

func (s *Service) ResumeRun(ctx context.Context, runID string) (*wfruntime.WorkflowRun, error) {
	run, err := s.LoadRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != wfruntime.StatusPaused {
		return nil, errors.New("run is not paused")
	}
	if s.controller != nil {
		s.controller.Clear(runID)
	}
	version, err := s.GetVersion(ctx, run.WorkflowVersionID)
	if err != nil {
		return nil, err
	}
	if s.store != nil {
		_ = s.store.AppendEvent(ctx, wfruntime.RunEvent{
			RunID:      run.ID,
			WorkflowID: run.WorkflowID,
			Type:       wfruntime.EventRunResumed,
			Status:     wfruntime.StatusRunning,
			Time:       time.Now(),
			Message:    "run resumed",
		})
	}
	return s.RunVersion(ctx, version, run)
}

func (s *Service) RunPublishedWorkflow(ctx context.Context, workflowID string, request *http.Request) (*wfruntime.WorkflowRun, error) {
	version, err := s.GetActiveVersion(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if version.Definition == nil || version.Definition.PublishConfig == nil || !version.Definition.PublishConfig.Enabled {
		return nil, errors.New("workflow is not published")
	}
	run := buildHTTPRun(version, request)
	return s.RunVersion(ctx, version, run)
}

func (s *Service) RunHTTPTrigger(ctx context.Context, workflowID string, request *http.Request) (*wfruntime.WorkflowRun, error) {
	version, err := s.GetActiveVersion(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if version.Definition == nil || !hasHTTPTrigger(version.Definition, request) {
		return nil, errors.New("http trigger not configured")
	}
	run := buildHTTPRun(version, request)
	return s.RunVersion(ctx, version, run)
}

func (s *Service) Store() wfruntime.Store {
	if s == nil {
		return nil
	}
	return s.store
}

func (s *Service) EmbeddedWorker() *worker.Service {
	if s == nil {
		return nil
	}
	return s.embeddedWorker
}

func (s *Service) WorkerRegistry() WorkerRegistry {
	if s == nil {
		return nil
	}
	return s.workers
}

func (s *Service) DefinitionRepository() definition.Repository {
	if s == nil {
		return nil
	}
	return s.definitions
}

func (s *Service) RunController() runner.RunController {
	if s == nil {
		return nil
	}
	return s.controller
}

func hasHTTPTrigger(def *definition.WorkflowDefinition, request *http.Request) bool {
	if def == nil || request == nil {
		return false
	}
	for _, trigger := range def.Triggers {
		if trigger.Type != definition.TriggerHTTP || !trigger.Enabled {
			continue
		}
		route, _ := trigger.Config["route"].(string)
		method, _ := trigger.Config["method"].(string)
		if route == request.URL.Path && (method == "" || method == request.Method) {
			return true
		}
	}
	return false
}

func buildHTTPRun(version *definition.WorkflowVersion, request *http.Request) *wfruntime.WorkflowRun {
	method := ""
	path := ""
	query := map[string][]string{}
	headers := map[string][]string{}
	var body any
	if request != nil {
		method = request.Method
		path = request.URL.Path
		query = request.URL.Query()
		headers = request.Header
	}
	if request != nil && request.Body != nil {
		defer request.Body.Close()
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		var payload any
		if err := decoder.Decode(&payload); err == nil {
			body = payload
		}
	}
	run := wfruntime.NewWorkflowRun("", version.WorkflowID, version.ID, "")
	run.Context.Variables["request"] = map[string]any{
		"method":  method,
		"path":    path,
		"query":   query,
		"headers": headers,
		"body":    body,
	}
	return run
}

func (s *Service) LoadRun(ctx context.Context, runID string) (*wfruntime.WorkflowRun, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("store is not configured")
	}
	return s.store.LoadRun(ctx, runID)
}

func (s *Service) ListRuns(ctx context.Context) ([]*wfruntime.WorkflowRun, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("store is not configured")
	}
	return s.store.ListRuns(ctx)
}

func (s *Service) RunEvents(ctx context.Context, runID string) ([]wfruntime.RunEvent, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("store is not configured")
	}
	return s.store.Events(ctx, runID)
}

func (s *Service) RunSnapshots(ctx context.Context, runID string) ([]*wfruntime.RunSnapshot, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("store is not configured")
	}
	return s.store.Snapshots(ctx, runID)
}

func (s *Service) ListWorkers(ctx context.Context) ([]workerproto.WorkerDescriptor, error) {
	if s == nil || s.workers == nil {
		return nil, errors.New("worker registry is not configured")
	}
	return s.workers.List(ctx)
}

func (s *Service) Handler() http.Handler {
	return NewHTTPHandler(s).Handler()
}
