package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/planning"
	"github.com/ninenhan/go-workflow/core/runner"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/core/workerproto"
	"github.com/ninenhan/go-workflow/worker"
)

type Options struct {
	Compiler               planning.Compiler
	Store                  wfruntime.Store
	Definitions            definition.Repository
	Workspace              definition.WorkspaceRepository
	RunController          runner.RunController
	EnableEmbeddedWorker   bool
	EmbeddedWorker         *worker.Service
	WorkerRegistry         WorkerRegistry
	DispatchMode           DispatchMode
	ResultReporter         runner.ResultReporter
	HeartbeatReporter      runner.HeartbeatReporter
	Credentials            credential.Store
	DefaultCredentialScope string
	Automations            AutomationStore
}

// Service is the orchestration entrypoint. It owns compilation, scheduling,
// runtime state, and optionally an embedded worker for single-binary deployments.
type Service struct {
	engine           *runner.Engine
	store            wfruntime.Store
	definitions      definition.Repository
	workspace        definition.WorkspaceRepository
	controller       runner.RunController
	workers          WorkerRegistry
	embeddedWorker   *worker.Service
	credentials      credential.Store
	credentialScope  string
	automations      AutomationStore
	activeRunCancels sync.Map
	runLifecycleMu   sync.Mutex
	shuttingDown     bool
	automationMu     sync.RWMutex
	automationCancel context.CancelFunc
	automationWake   chan struct{}
	automationDone   chan struct{}
	automationError  string
}

func NewService(opts Options) (*Service, error) {
	credentialScope := strings.TrimSpace(opts.DefaultCredentialScope)
	if credentialScope != "" {
		var err error
		credentialScope, err = credential.NormalizeScope(credentialScope)
		if err != nil {
			return nil, fmt.Errorf("default credential scope: %w", err)
		}
	}
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
	workspace := opts.Workspace
	if workspace == nil {
		workspace = definition.NewMemoryWorkspaceRepository()
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
				Enabled:            true,
				RegisterBuiltins:   true,
				CredentialResolver: opts.Credentials,
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
		engine:          engine,
		store:           store,
		definitions:     defs,
		workspace:       workspace,
		controller:      controller,
		workers:         workers,
		embeddedWorker:  embeddedWorker,
		credentials:     opts.Credentials,
		credentialScope: credentialScope,
		automations:     opts.Automations,
		automationWake:  make(chan struct{}, 1),
	}, nil
}

func (s *Service) CredentialStore() credential.Store {
	if s == nil {
		return nil
	}
	return s.credentials
}

func (s *Service) DefaultCredentialScope() string {
	if s == nil {
		return ""
	}
	return s.credentialScope
}

// ValidateVersion compiles a version into an execution plan without starting a run.
// The control plane uses this path to expose a cheap "can this definition run"
// check to the editor.
func (s *Service) ValidateVersion(ctx context.Context, version *definition.WorkflowVersion) (*planning.ExecutionPlan, error) {
	if s == nil || s.engine == nil {
		return nil, errors.New("scheduler service is not configured")
	}
	if s.engine.Compiler == nil {
		return nil, errors.New("compiler is not configured")
	}
	if version == nil || version.Definition == nil {
		return nil, errors.New("workflow version is nil")
	}
	if err := validateAutomationTriggers(version.Definition.Triggers); err != nil {
		return nil, err
	}
	return s.engine.Compiler.Compile(version)
}

// ValidateDefinition wraps an ad-hoc definition into an ephemeral version so the
// compiler validates it using the exact same rules as runtime execution.
func (s *Service) ValidateDefinition(ctx context.Context, def *definition.WorkflowDefinition) (*planning.ExecutionPlan, error) {
	if def == nil {
		return nil, errors.New("workflow definition is nil")
	}
	return s.ValidateVersion(ctx, ephemeralVersion(def))
}

func (s *Service) RunVersion(ctx context.Context, version *definition.WorkflowVersion, run *wfruntime.WorkflowRun) (*wfruntime.WorkflowRun, error) {
	return s.engine.RunVersion(ctx, version, run)
}

func (s *Service) RunDefinition(ctx context.Context, def *definition.WorkflowDefinition, run *wfruntime.WorkflowRun) (*wfruntime.WorkflowRun, error) {
	if def == nil {
		return nil, errors.New("workflow definition is nil")
	}
	return s.RunVersion(ctx, ephemeralVersion(def), run)
}

// StartVersion compiles and persists a pending run before executing it in the
// background. The returned value is an immutable snapshot safe for immediate
// HTTP serialization while the scheduler owns the live run instance.
func (s *Service) StartVersion(ctx context.Context, version *definition.WorkflowVersion, run *wfruntime.WorkflowRun) (*wfruntime.WorkflowRun, error) {
	return s.startVersion(ctx, version, run, 0)
}

func (s *Service) startVersion(ctx context.Context, version *definition.WorkflowVersion, run *wfruntime.WorkflowRun, timeout time.Duration) (*wfruntime.WorkflowRun, error) {
	if s == nil || s.engine == nil || s.engine.Compiler == nil || s.engine.Scheduler == nil {
		return nil, errors.New("scheduler service is not configured")
	}
	if s.store == nil {
		return nil, errors.New("store is not configured")
	}
	plan, err := s.engine.Compiler.Compile(version)
	if err != nil {
		return nil, err
	}
	clientSuppliedRunID := run != nil && strings.TrimSpace(run.ID) != ""
	prepared := runner.PrepareRun(plan, run)
	prepared.WorkflowID = plan.WorkflowID
	prepared.WorkflowVersionID = plan.WorkflowVersionID
	prepared.PlanID = plan.PlanID
	requestFingerprint, err := buildRunRequestFingerprint(prepared)
	if err != nil {
		return nil, err
	}
	prepared.RequestFingerprint = requestFingerprint
	executionCtx := context.WithoutCancel(ctx)
	var cancel context.CancelFunc
	if timeout > 0 {
		executionCtx, cancel = context.WithTimeout(executionCtx, timeout)
	} else {
		executionCtx, cancel = context.WithCancel(executionCtx)
	}
	s.runLifecycleMu.Lock()
	if s.shuttingDown {
		s.runLifecycleMu.Unlock()
		cancel()
		return nil, errors.New("scheduler service is shutting down")
	}
	if clientSuppliedRunID {
		existing, loadErr := s.store.LoadRun(ctx, prepared.ID)
		switch {
		case loadErr == nil:
			s.runLifecycleMu.Unlock()
			cancel()
			if existing.RequestFingerprint != requestFingerprint {
				return nil, errors.New("run id already belongs to a different workflow request")
			}
			return existing, nil
		case !errors.Is(loadErr, wfruntime.ErrRunNotFound):
			s.runLifecycleMu.Unlock()
			cancel()
			return nil, loadErr
		}
	}
	if _, loaded := s.activeRunCancels.LoadOrStore(prepared.ID, cancel); loaded {
		s.runLifecycleMu.Unlock()
		cancel()
		return nil, errors.New("run is already active")
	}
	if err := s.store.SaveRun(ctx, prepared); err != nil {
		s.activeRunCancels.Delete(prepared.ID)
		s.runLifecycleMu.Unlock()
		cancel()
		return nil, err
	}
	s.runLifecycleMu.Unlock()
	accepted := prepared.Clone()
	go func() {
		defer cancel()
		defer s.activeRunCancels.Delete(prepared.ID)
		_, _ = s.engine.Scheduler.Run(executionCtx, plan, prepared)
	}()
	return accepted, nil
}

func buildRunRequestFingerprint(run *wfruntime.WorkflowRun) (string, error) {
	payload, err := json.Marshal(struct {
		PlanID          string         `json:"plan_id"`
		CredentialScope string         `json:"credential_scope,omitempty"`
		Variables       map[string]any `json:"variables,omitempty"`
	}{
		PlanID:          run.PlanID,
		CredentialScope: run.CredentialScope,
		Variables:       run.Context.Variables,
	})
	if err != nil {
		return "", fmt.Errorf("marshal workflow run request identity: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) waitForRun(ctx context.Context, runID string) (*wfruntime.WorkflowRun, error) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		run, err := s.store.LoadRun(ctx, runID)
		if err != nil {
			return nil, err
		}
		switch run.Status {
		case wfruntime.StatusPending, wfruntime.StatusRunning, wfruntime.StatusRetry:
		default:
			return run, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	automationErr := s.stopAutomations(ctx)
	s.runLifecycleMu.Lock()
	s.shuttingDown = true
	s.activeRunCancels.Range(func(_, value any) bool {
		if cancel, ok := value.(context.CancelFunc); ok {
			cancel()
		}
		return true
	})
	s.runLifecycleMu.Unlock()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		active := false
		s.activeRunCancels.Range(func(_, _ any) bool {
			active = true
			return false
		})
		if !active {
			return automationErr
		}
		select {
		case <-ctx.Done():
			return errors.Join(automationErr, fmt.Errorf("shutdown scheduler service: %w", ctx.Err()))
		case <-ticker.C:
		}
	}
}

func (s *Service) StartDefinition(ctx context.Context, def *definition.WorkflowDefinition, run *wfruntime.WorkflowRun) (*wfruntime.WorkflowRun, error) {
	if def == nil {
		return nil, errors.New("workflow definition is nil")
	}
	return s.StartVersion(ctx, ephemeralVersion(def), run)
}

func (s *Service) StartVersionByID(ctx context.Context, versionID string, run *wfruntime.WorkflowRun) (*wfruntime.WorkflowRun, error) {
	version, err := s.GetVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}
	return s.StartVersion(ctx, version, run)
}

func ephemeralVersion(def *definition.WorkflowDefinition) *definition.WorkflowVersion {
	workflowID := def.ID
	if workflowID == "" {
		workflowID = "workflow"
	}
	return &definition.WorkflowVersion{
		ID:         workflowID + ":latest",
		WorkflowID: workflowID,
		Version:    1,
		Status:     definition.VersionPublished,
		Definition: def,
		CreatedAt:  time.Now(),
	}
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

func (s *Service) GetWorkspace(ctx context.Context) (*definition.Workspace, error) {
	if s == nil || s.workspace == nil {
		return nil, errors.New("workspace repository is not configured")
	}
	return s.workspace.GetWorkspace(ctx)
}

func (s *Service) SaveWorkspace(ctx context.Context, workspace *definition.Workspace, expectedRevision uint64) (*definition.Workspace, error) {
	if s == nil || s.workspace == nil {
		return nil, errors.New("workspace repository is not configured")
	}
	return s.workspace.SaveWorkspace(ctx, workspace, expectedRevision)
}

func (s *Service) CreateVersion(ctx context.Context, workflowID string, workflowDefinition *definition.WorkflowDefinition) (*definition.WorkflowVersion, error) {
	if s == nil || s.definitions == nil {
		return nil, errors.New("definition repository is not configured")
	}
	return s.definitions.CreateVersion(ctx, workflowID, workflowDefinition)
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
	version, err := s.definitions.GetVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}
	if _, err := s.ValidateVersion(ctx, version); err != nil {
		return nil, fmt.Errorf("validate workflow version: %w", err)
	}
	if err := s.validatePublishedRoutes(ctx, version); err != nil {
		return nil, err
	}
	published, err := s.definitions.PublishVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}
	s.notifyAutomationSync()
	return published, nil
}

type publishedRoute struct {
	path   string
	method string
	source string
}

func validatePublishAdapter(def *definition.WorkflowDefinition, config *definition.PublishConfig) error {
	if config == nil {
		return errors.New("published API configuration is required")
	}
	switch config.InputMode {
	case "", "request", "body", "query":
	default:
		return fmt.Errorf("published API input_mode %q is not supported", config.InputMode)
	}
	switch config.ResponseMode {
	case "", "run":
	case "result":
		found := false
		for _, node := range def.Nodes {
			if !node.Disabled && node.Executor.Ref == "TerminalUnit" {
				found = true
				break
			}
		}
		if !found {
			return errors.New("published API response_mode result requires a Return Result action")
		}
	default:
		return fmt.Errorf("published API response_mode %q is not supported", config.ResponseMode)
	}
	if config.TimeoutMS < 0 || config.TimeoutMS > int64((24*time.Hour)/time.Millisecond) {
		return errors.New("published API timeout must be between 0 and 86400000 milliseconds")
	}
	if _, err := parsePublishedAPIInputs(def); err != nil {
		return fmt.Errorf("published API input contract: %w", err)
	}
	return nil
}

func publishedWorkflowResult(def *definition.WorkflowDefinition, run *wfruntime.WorkflowRun) (any, error) {
	if def == nil || run == nil {
		return nil, errors.New("published workflow result is unavailable")
	}
	matched := make([]string, 0, 1)
	for _, node := range def.Nodes {
		if node.Disabled || node.Executor.Ref != "TerminalUnit" {
			continue
		}
		if _, executed := run.Context.NodeResults[node.ID]; executed {
			matched = append(matched, node.ID)
		}
	}
	if len(matched) == 0 {
		return nil, errors.New("published workflow did not execute a Return Result action")
	}
	if len(matched) > 1 {
		return nil, errors.New("published workflow executed more than one Return Result action")
	}
	return run.Context.NodeResults[matched[0]], nil
}

func workflowPublishedRoutes(def *definition.WorkflowDefinition) ([]publishedRoute, error) {
	if def == nil {
		return nil, errors.New("workflow definition is required")
	}
	routes := make([]publishedRoute, 0, len(def.Triggers)+1)
	appendRoute := func(path, method, source string) error {
		path = strings.TrimSpace(path)
		method = strings.ToUpper(strings.TrimSpace(method))
		if path == "" || !strings.HasPrefix(path, "/") {
			return fmt.Errorf("%s route must start with /", source)
		}
		if path == "/v1" || strings.HasPrefix(path, "/v1/") {
			return fmt.Errorf("%s route conflicts with the control API", source)
		}
		if method == "" {
			return fmt.Errorf("%s method is required", source)
		}
		routes = append(routes, publishedRoute{path: path, method: method, source: source})
		return nil
	}
	if config := def.PublishConfig; config != nil && config.Enabled {
		if config.AuthRequired {
			return nil, errors.New("published API authentication is not configured")
		}
		if err := validatePublishAdapter(def, config); err != nil {
			return nil, err
		}
		if err := appendRoute(config.Route, config.Method, "published API"); err != nil {
			return nil, err
		}
	}
	for _, trigger := range def.Triggers {
		if trigger.Type != definition.TriggerHTTP || !trigger.Enabled {
			continue
		}
		route, _ := trigger.Config["route"].(string)
		method, _ := trigger.Config["method"].(string)
		if err := appendRoute(route, method, fmt.Sprintf("HTTP trigger %s", trigger.ID)); err != nil {
			return nil, err
		}
	}
	return routes, nil
}

func routeConflicts(left, right publishedRoute) bool {
	return left.path == right.path && left.method == right.method
}

func (s *Service) validatePublishedRoutes(ctx context.Context, target *definition.WorkflowVersion) error {
	targetRoutes, err := workflowPublishedRoutes(target.Definition)
	if err != nil {
		return err
	}
	for index, route := range targetRoutes {
		for _, candidate := range targetRoutes[index+1:] {
			if routeConflicts(route, candidate) {
				return fmt.Errorf("route %s %s is used by both %s and %s", route.method, route.path, route.source, candidate.source)
			}
		}
	}

	workflows, err := s.definitions.ListWorkflows(ctx)
	if err != nil {
		return err
	}
	for _, workflow := range workflows {
		if workflow.ID == target.WorkflowID || workflow.ActiveVersion == "" {
			continue
		}
		active, activeErr := s.definitions.GetActiveVersion(ctx, workflow.ID)
		if activeErr != nil {
			return activeErr
		}
		activeRoutes, routeErr := workflowPublishedRoutes(active.Definition)
		if routeErr != nil {
			return fmt.Errorf("published workflow %s is invalid: %w", workflow.ID, routeErr)
		}
		for _, route := range targetRoutes {
			for _, candidate := range activeRoutes {
				if routeConflicts(route, candidate) {
					return fmt.Errorf("route %s %s is already published by workflow %s", route.method, route.path, workflow.ID)
				}
			}
		}
	}
	return nil
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
	if cancel, ok := s.activeRunCancels.Load(runID); ok {
		cancel.(context.CancelFunc)()
	}
	return run, nil
}

func (s *Service) ResumeRun(ctx context.Context, runID string) (*wfruntime.WorkflowRun, error) {
	run, err := s.LoadRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != wfruntime.StatusPaused && run.Status != wfruntime.StatusRunning {
		return nil, errors.New("run is neither paused nor interrupted")
	}
	if _, active := s.activeRunCancels.Load(runID); active {
		return nil, errors.New("run is already active")
	}
	if s.controller != nil {
		s.controller.Clear(runID)
	}
	version, err := s.GetVersion(ctx, run.WorkflowVersionID)
	if err != nil {
		return nil, err
	}
	return s.RunVersion(ctx, version, run)
}

func (s *Service) RunPublishedWorkflow(ctx context.Context, workflowID string, request *http.Request) (*wfruntime.WorkflowRun, error) {
	version, err := s.GetActiveVersion(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	return s.RunPublishedVersion(ctx, version, request)
}

func (s *Service) RunPublishedVersion(ctx context.Context, version *definition.WorkflowVersion, request *http.Request) (*wfruntime.WorkflowRun, error) {
	run, err := s.buildPublishedRun(version, request)
	if err != nil {
		return nil, err
	}
	if timeout := version.Definition.PublishConfig.TimeoutMS; timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
		defer cancel()
	}
	return s.RunVersion(ctx, version, run)
}

// StartPublishedVersion validates published input and starts the same immutable
// version used by synchronous published calls.
func (s *Service) StartPublishedVersion(ctx context.Context, version *definition.WorkflowVersion, request *http.Request) (*wfruntime.WorkflowRun, error) {
	run, err := s.buildPublishedRun(version, request)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(version.Definition.PublishConfig.TimeoutMS) * time.Millisecond
	return s.startVersion(ctx, version, run, timeout)
}

func (s *Service) buildPublishedRun(version *definition.WorkflowVersion, request *http.Request) (*wfruntime.WorkflowRun, error) {
	if version == nil {
		return nil, errors.New("published workflow version is required")
	}
	if version.Definition == nil || version.Definition.PublishConfig == nil || !version.Definition.PublishConfig.Enabled {
		return nil, errors.New("workflow is not published")
	}
	run, err := buildHTTPRun(version, request, version.Definition.PublishConfig.InputMode)
	if err != nil {
		return nil, err
	}
	if s.credentialScope != "" {
		run.CredentialScope = s.credentialScope
	}
	return run, nil
}

func (s *Service) RunHTTPTrigger(ctx context.Context, workflowID string, request *http.Request) (*wfruntime.WorkflowRun, error) {
	version, err := s.GetActiveVersion(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if version.Definition == nil || !hasHTTPTrigger(version.Definition, request) {
		return nil, errors.New("http trigger not configured")
	}
	run, err := buildHTTPRun(version, request, "request")
	if err != nil {
		return nil, err
	}
	if s.credentialScope != "" {
		run.CredentialScope = s.credentialScope
	}
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

func buildHTTPRun(version *definition.WorkflowVersion, request *http.Request, inputMode string) (*wfruntime.WorkflowRun, error) {
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
		} else if !errors.Is(err, io.EOF) && inputMode == "body" {
			return nil, errors.New("published API body must contain valid JSON")
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
	switch inputMode {
	case "", "request":
	case "body":
		if body == nil {
			break
		}
		fields, ok := body.(map[string]any)
		if !ok {
			return nil, errors.New("published API body must be a JSON object")
		}
		if err := mergePublishedInput(run.Context.Variables, fields); err != nil {
			return nil, err
		}
	case "query":
		fields := make(map[string]any, len(query))
		for key, values := range query {
			if len(values) == 1 {
				fields[key] = values[0]
			} else {
				fields[key] = append([]string(nil), values...)
			}
		}
		if err := mergePublishedInput(run.Context.Variables, fields); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported published API input_mode %q", inputMode)
	}
	if err := applyPublishedInputContract(version.Definition, run.Context.Variables, inputMode); err != nil {
		return nil, err
	}
	return run, nil
}

func mergePublishedInput(variables map[string]any, fields map[string]any) error {
	if _, reserved := fields["request"]; reserved {
		return errors.New("published API input key request is reserved")
	}
	for key, value := range fields {
		key = strings.TrimSpace(key)
		if key == "" {
			return errors.New("published API input keys must not be empty")
		}
		variables[key] = value
	}
	return nil
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
