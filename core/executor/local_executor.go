package executor

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrLocalFuncAlreadyRegistered = errors.New("local function already registered")

type LocalFunc func(ctx context.Context, req Request) (Result, error)

type LocalExecutor struct {
	mu    sync.RWMutex
	funcs map[string]LocalFunc
}

func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{funcs: make(map[string]LocalFunc)}
}

func (e *LocalExecutor) Type() Type { return TypeLocalGo }

func (e *LocalExecutor) Register(name string, fn LocalFunc) error {
	if e == nil {
		return fmt.Errorf("local executor is nil")
	}
	if name == "" {
		return fmt.Errorf("local function name is empty")
	}
	if fn == nil {
		return fmt.Errorf("local function %s is nil", name)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.funcs == nil {
		e.funcs = make(map[string]LocalFunc)
	}
	if _, exists := e.funcs[name]; exists {
		return fmt.Errorf("%w: %s", ErrLocalFuncAlreadyRegistered, name)
	}
	e.funcs[name] = fn
	return nil
}

func (e *LocalExecutor) RegisterOrReplace(name string, fn LocalFunc) error {
	if e == nil {
		return fmt.Errorf("local executor is nil")
	}
	if name == "" {
		return fmt.Errorf("local function name is empty")
	}
	if fn == nil {
		return fmt.Errorf("local function %s is nil", name)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.funcs == nil {
		e.funcs = make(map[string]LocalFunc)
	}
	e.funcs[name] = fn
	return nil
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
