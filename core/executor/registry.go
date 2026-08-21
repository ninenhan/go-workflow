package executor

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var ErrAlreadyRegistered = errors.New("executor already registered")

type Registry struct {
	mu        sync.RWMutex
	executors map[Type]Executor
}

func NewRegistry() *Registry {
	return &Registry{executors: make(map[Type]Executor)}
}

func (r *Registry) Register(exec Executor) error {
	return r.RegisterAll(exec)
}

// RegisterAll validates the complete batch before changing the registry. This
// prevents a partially installed executor set when one item conflicts.
func (r *Registry) RegisterAll(executors ...Executor) error {
	if r == nil {
		return fmt.Errorf("executor registry is nil")
	}
	batch := make(map[Type]Executor, len(executors))
	for _, exec := range executors {
		if exec == nil {
			return fmt.Errorf("executor is nil")
		}
		t := exec.Type()
		if t == "" {
			return fmt.Errorf("executor type is empty")
		}
		if _, exists := batch[t]; exists {
			return fmt.Errorf("%w in batch: %s", ErrAlreadyRegistered, t)
		}
		batch[t] = exec
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.executors == nil {
		r.executors = make(map[Type]Executor)
	}
	for t := range batch {
		if _, exists := r.executors[t]; exists {
			return fmt.Errorf("%w: %s", ErrAlreadyRegistered, t)
		}
	}
	for t, exec := range batch {
		r.executors[t] = exec
	}
	return nil
}

// RegisterOrReplace makes replacement an explicit operation. Prefer Register
// during normal composition so accidental type collisions fail fast.
func (r *Registry) RegisterOrReplace(exec Executor) error {
	if r == nil {
		return fmt.Errorf("executor registry is nil")
	}
	if exec == nil {
		return fmt.Errorf("executor is nil")
	}
	t := exec.Type()
	if t == "" {
		return fmt.Errorf("executor type is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.executors == nil {
		r.executors = make(map[Type]Executor)
	}
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
