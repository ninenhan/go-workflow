package unit

import (
	"context"
	"fmt"
	"reflect"
	"sync"
)

type ContextMap map[string]*ExecutionResult

type Unit struct {
	ID          string `json:"id"`
	UnitName    string `json:"unit_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Status      string `json:"status,omitempty"`
	ErrMsg      string `json:"err_msg,omitempty"`
	OutputRef   string `json:"output_ref,omitempty"`
}

type Executable interface {
	GetUnitMeta() *Unit
	Execute(ctx context.Context, state ContextMap, self *Node) (*ExecutionResult, error)
}

type ExecutableUnit = Executable

type Input struct {
	Data      any    `json:"data,omitempty"`
	DataType  string `json:"data_type,omitempty"`
	Slottable bool   `json:"slottable,omitempty"`
}

type ControlSignal string

const (
	ControlBreak    ControlSignal = "break"
	ControlContinue ControlSignal = "continue"
	ControlGoto     ControlSignal = "goto"
)

type ControlSignalError struct {
	Signal ControlSignal
	Target string
}

func (e *ControlSignalError) Error() string {
	if e == nil {
		return ""
	}
	if e.Target != "" {
		return fmt.Sprintf("control signal: %s -> %s", e.Signal, e.Target)
	}
	return fmt.Sprintf("control signal: %s", e.Signal)
}

type ExecutionResult struct {
	NodeName      string        `json:"node_name,omitempty"`
	Data          any           `json:"data,omitempty"`
	Stream        bool          `json:"stream,omitempty"`
	Raw           any           `json:"raw,omitempty"`
	Error         string        `json:"error,omitempty"`
	Control       ControlSignal `json:"control,omitempty"`
	ControlTarget string        `json:"control_target,omitempty"`
}

func SimpleResult(data any) *ExecutionResult {
	return &ExecutionResult{Data: data}
}

type Node struct {
	ID           string         `json:"id,omitempty"`
	Name         string         `json:"name,omitempty"`
	Input        *Input         `json:"input,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
	ExportFields []string       `json:"export_fields,omitempty"`
}

type Factory func() Executable

type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

func (r *Registry) Register(name string, factory Factory) {
	if r == nil || name == "" || factory == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.factories == nil {
		r.factories = make(map[string]Factory)
	}
	r.factories[name] = factory
}

func (r *Registry) RegisterUnitFactory(name string, factory Factory) {
	r.Register(name, factory)
}

func (r *Registry) RegisterInstance(name string, exec Executable) {
	if exec == nil || name == "" {
		return
	}
	t := reflect.TypeOf(exec)
	r.Register(name, func() Executable {
		if t == nil {
			return normalize(exec)
		}
		if t.Kind() == reflect.Ptr {
			v := reflect.New(t.Elem()).Interface()
			if next, ok := v.(Executable); ok {
				return normalize(next)
			}
			return normalize(exec)
		}
		v := reflect.New(t).Elem().Interface()
		if next, ok := v.(Executable); ok {
			return normalize(next)
		}
		return normalize(exec)
	})
}

func (r *Registry) RegisterUnit(name string, exec Executable) {
	r.RegisterInstance(name, exec)
}

func (r *Registry) New(name string) (Executable, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	factory := r.factories[name]
	r.mu.RUnlock()
	if factory == nil {
		return nil, false
	}
	return normalize(factory()), true
}

func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}

var DefaultRegistry = NewRegistry()

func RegisterFactory(name string, factory Factory) {
	DefaultRegistry.Register(name, factory)
}

func RegisterUnitFactory(name string, factory Factory) {
	RegisterFactory(name, factory)
}

func Register(name string, exec Executable) {
	DefaultRegistry.RegisterInstance(name, exec)
}

func RegisterUnit(name string, exec Executable) {
	Register(name, exec)
}

func Find(name string) (Executable, bool) {
	return DefaultRegistry.New(name)
}

func normalize(exec Executable) Executable {
	if exec == nil {
		return nil
	}
	meta := exec.GetUnitMeta()
	if meta != nil && meta.UnitName == "" {
		if namer, ok := exec.(interface{ GetUnitName() string }); ok {
			meta.UnitName = namer.GetUnitName()
		}
	}
	return exec
}
