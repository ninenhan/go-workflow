package worker

import (
	"fmt"
	"sort"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
	_ "github.com/ninenhan/go-workflow/units"
	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

type Options struct {
	Enabled          bool
	RegisterBuiltins bool
	Registry         *executor.Registry
	UnitRegistry     *workerunit.Registry
}

// Service is the embedded worker runtime. It can be disabled when the deployment
// wants the scheduler to delegate execution to standalone worker processes only.
type Service struct {
	enabled      bool
	registry     *executor.Registry
	unitRegistry *workerunit.Registry
}

func NewService(opts Options) (*Service, error) {
	reg := opts.Registry
	if reg == nil {
		reg = executor.NewRegistry()
	}
	svc := &Service{
		enabled:      opts.Enabled,
		registry:     reg,
		unitRegistry: opts.UnitRegistry,
	}
	if svc.unitRegistry == nil {
		svc.unitRegistry = workerunit.DefaultRegistry
	}
	if !svc.enabled {
		return svc, nil
	}
	if opts.RegisterBuiltins {
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

func (s *Service) registerBuiltins() error {
	builtins := []executor.Executor{
		executor.NewLocalExecutor(),
		executor.NewHTTPExecutor(nil),
		executor.NewScriptExecutor(),
		workerunit.NewExecutor(s.unitRegistry),
		&executor.ContainerExecutor{},
	}
	for _, exec := range builtins {
		if err := s.registry.Register(exec); err != nil {
			return err
		}
	}
	return nil
}

func appendIfMissing(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}
