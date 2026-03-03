package workflow

import (
	"reflect"
	"sync"
)

type UnitFactory func() ExecutableUnit

type Registry struct {
	mu        sync.RWMutex
	factories map[string]UnitFactory
}

func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]UnitFactory),
	}
}

func (r *Registry) Register(name string, factory UnitFactory) {
	if factory == nil || name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.factories == nil {
		r.factories = make(map[string]UnitFactory)
	}
	r.factories[name] = factory
}

// RegisterInstance registers a unit by inferring a safe factory via reflection.
// Prefer Register with an explicit factory for full control.
func (r *Registry) RegisterInstance(name string, unit ExecutableUnit) {
	if unit == nil || name == "" {
		return
	}
	t := reflect.TypeOf(unit)
	factory := func() ExecutableUnit {
		if t == nil {
			return normalizeUnit(unit)
		}
		if t.Kind() == reflect.Ptr {
			v := reflect.New(t.Elem()).Interface()
			if u, ok := v.(ExecutableUnit); ok {
				return normalizeUnit(u)
			}
			return normalizeUnit(unit)
		}
		v := reflect.New(t).Elem().Interface()
		if u, ok := v.(ExecutableUnit); ok {
			return normalizeUnit(u)
		}
		return normalizeUnit(unit)
	}
	r.Register(name, factory)
}

func (r *Registry) New(name string) (ExecutableUnit, bool) {
	r.mu.RLock()
	factory := r.factories[name]
	r.mu.RUnlock()
	if factory == nil {
		return nil, false
	}
	return normalizeUnit(factory()), true
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for k := range r.factories {
		names = append(names, k)
	}
	return names
}

var DefaultRegistry = NewRegistry()

// RegisterUnit keeps backward compatibility with the old API.
func RegisterUnit(name string, unit ExecutableUnit) {
	DefaultRegistry.RegisterInstance(name, unit)
}

// RegisterUnitFactory registers a factory for a unit.
func RegisterUnitFactory(name string, factory UnitFactory) {
	DefaultRegistry.Register(name, factory)
}

// FindUnit returns a fresh unit instance if registered.
func FindUnit(name string) (ExecutableUnit, bool) {
	return DefaultRegistry.New(name)
}

func normalizeUnit(unit ExecutableUnit) ExecutableUnit {
	if unit == nil {
		return nil
	}
	meta := unit.GetUnitMeta()
	if meta != nil && meta.UnitName == "" {
		if namer, ok := unit.(interface{ GetUnitName() string }); ok {
			meta.UnitName = namer.GetUnitName()
		}
	}
	return unit
}
