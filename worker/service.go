package worker

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
	"github.com/ninenhan/go-workflow/units"
	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

type Options struct {
	Enabled            bool
	RegisterBuiltins   bool
	Registry           *executor.Registry
	UnitRegistry       *workerunit.Registry
	CredentialResolver credential.Resolver
}

// Service is the embedded worker runtime. It can be disabled when the deployment
// wants the scheduler to delegate execution to standalone worker processes only.
type Service struct {
	enabled      bool
	registry     *executor.Registry
	unitRegistry *workerunit.Registry
	credentials  credential.Resolver
	executionMu  sync.Mutex
	executions   map[string]*executionEntry
	executionLog []string
}

type executionEntry struct {
	done   chan struct{}
	result executor.ExecuteResult
	err    error
}

const executionCacheLimit = 2048

func NewService(opts Options) (*Service, error) {
	reg := opts.Registry
	if reg == nil {
		reg = executor.NewRegistry()
	}
	svc := &Service{
		enabled:      opts.Enabled,
		registry:     reg,
		unitRegistry: opts.UnitRegistry,
		credentials:  opts.CredentialResolver,
		executions:   make(map[string]*executionEntry),
	}
	if svc.unitRegistry == nil {
		// Clone legacy global registrations once for compatibility, then keep all
		// service mutations isolated.
		svc.unitRegistry = workerunit.DefaultRegistry.Clone()
	}
	if svc.credentials == nil {
		svc.credentials = credential.EnvironmentResolver{}
	}
	if !svc.enabled {
		return svc, nil
	}
	if opts.RegisterBuiltins {
		if err := units.RegisterBuiltins(svc.unitRegistry); err != nil {
			return nil, err
		}
		if err := svc.registerBuiltins(); err != nil {
			return nil, err
		}
	}
	return svc, nil
}

func (s *Service) Enabled() bool {
	return s != nil && s.enabled
}

func (s *Service) Registry() *executor.Registry {
	if s == nil {
		return nil
	}
	return s.registry
}

func (s *Service) UnitRegistry() *workerunit.Registry {
	if s == nil {
		return nil
	}
	return s.unitRegistry
}

func (s *Service) RegisterExecutor(exec executor.Executor) error {
	if s == nil {
		return fmt.Errorf("worker service is nil")
	}
	if !s.enabled {
		return fmt.Errorf("worker service is disabled")
	}
	return s.registry.Register(exec)
}

func (s *Service) Descriptor(base workerproto.WorkerDescriptor) workerproto.WorkerDescriptor {
	if s == nil {
		return base
	}
	if base.Status == "" {
		base.Status = workerproto.StatusOnline
	}
	if base.Weight <= 0 {
		base.Weight = 1
	}
	if base.Transport == "" {
		base.Transport = workerproto.TransportCallback
	}
	if base.ProtocolVersion == "" {
		base.ProtocolVersion = workerproto.ProtocolVersion
	}
	for _, t := range s.registry.Types() {
		base.SupportedExecutorTypes = appendIfMissing(base.SupportedExecutorTypes, string(t))
	}
	for _, name := range s.unitRegistry.Names() {
		base.SupportedExecutorRefs = appendIfMissing(base.SupportedExecutorRefs, name)
	}
	sort.Strings(base.SupportedExecutorTypes)
	sort.Strings(base.SupportedExecutorRefs)
	return base
}

// executeOnce makes dispatch_id the idempotency boundary for every transport.
// Concurrent redeliveries wait for the original execution and receive the exact
// same result instead of invoking user code twice.
func (s *Service) executeOnce(ctx context.Context, task executor.ExecuteTask, execute func() (executor.ExecuteResult, error)) (executor.ExecuteResult, error) {
	if task.DispatchID == "" {
		return execute()
	}
	s.executionMu.Lock()
	if entry := s.executions[task.DispatchID]; entry != nil {
		s.executionMu.Unlock()
		select {
		case <-ctx.Done():
			return executor.ExecuteResult{}, ctx.Err()
		case <-entry.done:
			return entry.result, entry.err
		}
	}
	entry := &executionEntry{done: make(chan struct{})}
	s.executions[task.DispatchID] = entry
	s.executionLog = append(s.executionLog, task.DispatchID)
	s.executionMu.Unlock()

	result, err := execute()

	s.executionMu.Lock()
	entry.result = result
	entry.err = err
	close(entry.done)
	s.trimExecutionCacheLocked()
	s.executionMu.Unlock()
	return result, err
}

func (s *Service) trimExecutionCacheLocked() {
	for len(s.executionLog) > executionCacheLimit {
		oldest := s.executionLog[0]
		entry := s.executions[oldest]
		if entry != nil {
			select {
			case <-entry.done:
				delete(s.executions, oldest)
			default:
				return
			}
		}
		s.executionLog = s.executionLog[1:]
	}
}

func (s *Service) registerBuiltins() error {
	builtins := []executor.Executor{
		executor.NewLocalExecutor(),
		executor.NewHTTPExecutor(nil),
		executor.NewScriptExecutor(),
		workerunit.NewExecutorWithCredentials(s.unitRegistry, s.credentials),
	}
	return s.registry.RegisterAll(builtins...)
}

func appendIfMissing(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}
