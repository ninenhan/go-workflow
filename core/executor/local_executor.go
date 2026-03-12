package executor

import (
	"context"
	"fmt"
	"sync"
)

type LocalFunc func(ctx context.Context, req Request) (Result, error)

type LocalExecutor struct {
	mu    sync.RWMutex
	funcs map[string]LocalFunc
}

func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{funcs: make(map[string]LocalFunc)}
}

func (e *LocalExecutor) Type() Type { return TypeLocalGo }

func (e *LocalExecutor) Register(name string, fn LocalFunc) {
	if name == "" || fn == nil {
		return
	}
	e.mu.Lock()
	e.funcs[name] = fn
	e.mu.Unlock()
}

func (e *LocalExecutor) Execute(ctx context.Context, req Request) (Result, error) {
	name, _ := req.Params["fn"].(string)
	if name == "" {
		return Result{}, fmt.Errorf("local executor missing fn")
	}
	e.mu.RLock()
	fn := e.funcs[name]
	e.mu.RUnlock()
	if fn == nil {
		return Result{}, fmt.Errorf("local function not found: %s", name)
	}
	res, err := fn(ctx, req)
	if err != nil {
		return res, err
	}
	if res.Status == "" {
		res.Status = StatusSucceeded
	}
	return res, nil
}
