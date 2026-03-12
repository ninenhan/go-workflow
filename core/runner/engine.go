package runner

import (
	"context"
	"errors"

	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/core/planning"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
)

// Engine is the new runtime facade: definition -> plan -> run.
type Engine struct {
	Compiler  planning.Compiler
	Scheduler Scheduler
}

func NewEngine(compiler planning.Compiler, scheduler Scheduler) *Engine {
	if compiler == nil {
		compiler = planning.NewCompiler()
	}
	return &Engine{Compiler: compiler, Scheduler: scheduler}
}

func (e *Engine) RunVersion(ctx context.Context, version *definition.WorkflowVersion, run *wfruntime.WorkflowRun) (*wfruntime.WorkflowRun, error) {
	if e == nil {
		return nil, errors.New("engine is nil")
	}
	if e.Compiler == nil {
		return nil, errors.New("compiler is nil")
	}
	if e.Scheduler == nil {
		return nil, errors.New("scheduler is nil")
	}
	plan, err := e.Compiler.Compile(version)
	if err != nil {
		return nil, err
	}
	return e.Scheduler.Run(ctx, plan, run)
}
