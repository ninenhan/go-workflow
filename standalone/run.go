package standalone

import (
	"context"
	"errors"
	"fmt"

	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/scheduler"
	"github.com/ninenhan/go-workflow/worker"
)

type RunInput struct {
	Vars    map[string]any
	Request map[string]any
}

type RunnerConfig struct {
	Definition    *definition.WorkflowDefinition
	RegisterUnits func(*worker.Service) error
}

func RunDefinition(ctx context.Context, cfg RunnerConfig, input RunInput) (*wfruntime.WorkflowRun, error) {
	if cfg.Definition == nil {
		return nil, errors.New("workflow definition is nil")
	}

	workerSvc, err := worker.NewService(worker.Options{
		Enabled:          true,
		RegisterBuiltins: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create embedded worker: %w", err)
	}
	if cfg.RegisterUnits != nil {
		if err := cfg.RegisterUnits(workerSvc); err != nil {
			return nil, fmt.Errorf("register custom units: %w", err)
		}
	}

	svc, err := scheduler.NewService(scheduler.Options{
		EnableEmbeddedWorker: true,
		EmbeddedWorker:       workerSvc,
	})
	if err != nil {
		return nil, fmt.Errorf("create scheduler service: %w", err)
	}

	run := wfruntime.NewWorkflowRun("", "", "", "")
	for key, value := range input.Vars {
		run.Context.Variables[key] = value
	}
	if len(input.Request) > 0 {
		run.Context.Variables["request"] = cloneMap(input.Request)
	}

	return svc.RunDefinition(ctx, cfg.Definition, run)
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
