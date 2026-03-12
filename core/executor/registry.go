package executor

import (
	"fmt"
	"sort"
	"sync"
)

type Registry struct {
	mu        sync.RWMutex
	executors map[Type]Executor
}

func NewRegistry() *Registry {
	return &Registry{executors: make(map[Type]Executor)}
}

func (r *Registry) Register(exec Executor) error {
	if exec == nil {
		return fmt.Errorf("executor is nil")
	}
	t := exec.Type()
	if t == "" {
		return fmt.Errorf("executor type is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executors[t] = exec
	return nil
}

func (r *Registry) MustRegister(exec Executor) {
	if err := r.Register(exec); err != nil {
		panic(err)
	}
}

func (r *Registry) Get(t Type) (Executor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	exec, ok := r.executors[t]
	return exec, ok
}

func (r *Registry) Types() []Type {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Type, 0, len(r.executors))
	for t := range r.executors {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
