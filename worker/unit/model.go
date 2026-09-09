package unit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
)

var ErrAlreadyRegistered = errors.New("unit already registered")

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
	// An omitted status preserves synchronous success for existing units.
	// Accepted and running require ExternalTaskID and a persistent async store.
	Status         executor.Status `json:"status,omitempty"`
	ExternalTaskID string          `json:"external_task_id,omitempty"`
	// Set either TTL or ExpireAt to limit callback waiting, or neither to wait
	// indefinitely. TTL uses time.Duration, not a number of seconds.
	TTL             time.Duration  `json:"ttl,omitempty"`
	ExpireAt        time.Time      `json:"expire_at,omitempty,omitzero"`
	NodeName        string         `json:"node_name,omitempty"`
	Data            any            `json:"data,omitempty"`
	Variables       map[string]any `json:"variables,omitempty"`
	DeleteVariables []string       `json:"delete_variables,omitempty"`
	Stream          bool           `json:"stream,omitempty"`
	Raw             any            `json:"raw,omitempty"`
	Error           string         `json:"error,omitempty"`
	Control         ControlSignal  `json:"control,omitempty"`
	ControlTarget   string         `json:"control_target,omitempty"`
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

type Registration struct {
	Name    string
	Factory Factory
}

type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

func (r *Registry) Register(name string, factory Factory) error {
	return r.RegisterAll(Registration{Name: name, Factory: factory})
}

// RegisterAll validates the complete batch before changing the registry.
func (r *Registry) RegisterAll(registrations ...Registration) error {
	if r == nil {
		return fmt.Errorf("unit registry is nil")
	}
	batch := make(map[string]Factory, len(registrations))
	for _, registration := range registrations {
		if registration.Name == "" {
			return fmt.Errorf("unit name is empty")
		}
		if registration.Factory == nil {
			return fmt.Errorf("unit factory %s is nil", registration.Name)
		}
		if _, exists := batch[registration.Name]; exists {
			return fmt.Errorf("%w in batch: %s", ErrAlreadyRegistered, registration.Name)
		}
		batch[registration.Name] = registration.Factory
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.factories == nil {
		r.factories = make(map[string]Factory)
	}
	for name := range batch {
		if _, exists := r.factories[name]; exists {
			return fmt.Errorf("%w: %s", ErrAlreadyRegistered, name)
		}
	}
	for name, factory := range batch {
		r.factories[name] = factory
	}
	return nil
}

func (r *Registry) RegisterUnitFactory(name string, factory Factory) error {
	return r.Register(name, factory)
}

func (r *Registry) RegisterInstance(name string, exec Executable) error {
	if exec == nil {
		return fmt.Errorf("unit instance %s is nil", name)
	}
	t := reflect.TypeOf(exec)
	return r.Register(name, func() Executable {
		if t.Kind() == reflect.Ptr {
			v := reflect.New(t.Elem()).Interface()
			if next, ok := v.(Executable); ok {
				return normalize(next)
			}
			return nil
		}
		v := reflect.New(t).Elem().Interface()
		if next, ok := v.(Executable); ok {
			return normalize(next)
		}
		return nil
	})
}

func (r *Registry) RegisterUnit(name string, exec Executable) error {
	return r.RegisterInstance(name, exec)
}

// RegisterOrReplace makes replacement explicit for applications that support
// hot swapping. Normal startup composition should use Register.
func (r *Registry) RegisterOrReplace(name string, factory Factory) error {
	if r == nil {
		return fmt.Errorf("unit registry is nil")
	}
	if name == "" {
		return fmt.Errorf("unit name is empty")
	}
	if factory == nil {
		return fmt.Errorf("unit factory %s is nil", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.factories == nil {
		r.factories = make(map[string]Factory)
	}
	r.factories[name] = factory
	return nil
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
	exec := normalize(factory())
	if exec == nil {
		return nil, false
	}
	return exec, true
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
	sort.Strings(names)
	return names
}

// Registrations returns a stable snapshot that can be installed into another
// registry without sharing mutable registry state.
func (r *Registry) Registrations() []Registration {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	registrations := make([]Registration, 0, len(names))
	for _, name := range names {
		registrations = append(registrations, Registration{Name: name, Factory: r.factories[name]})
	}
	return registrations
}

func (r *Registry) Clone() *Registry {
	cloned := NewRegistry()
	for _, registration := range r.Registrations() {
		cloned.factories[registration.Name] = registration.Factory
	}
	return cloned
}

// DefaultRegistry is retained for source compatibility with applications that
// registered units globally. New worker services clone it during construction
// and never share its mutable state.
//
// Deprecated: create a Registry and pass it through worker.Options.UnitRegistry.
var DefaultRegistry = NewRegistry()

// Deprecated: register on an application-owned Registry instead.
func RegisterFactory(name string, factory Factory) error {
	return DefaultRegistry.Register(name, factory)
}

// Deprecated: register on an application-owned Registry instead.
func RegisterUnitFactory(name string, factory Factory) error {
	return RegisterFactory(name, factory)
}

// Deprecated: register on an application-owned Registry instead.
func Register(name string, exec Executable) error {
	return DefaultRegistry.RegisterInstance(name, exec)
}

// Deprecated: register on an application-owned Registry instead.
func RegisterUnit(name string, exec Executable) error {
	return Register(name, exec)
}

// Deprecated: resolve from an application-owned Registry instead.
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
