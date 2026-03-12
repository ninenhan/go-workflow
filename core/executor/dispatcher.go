package executor

import "fmt"

type RegistryDispatcher struct {
	Registry *Registry
}

func NewRegistryDispatcher(reg *Registry) *RegistryDispatcher {
	if reg == nil {
		reg = NewRegistry()
	}
	return &RegistryDispatcher{Registry: reg}
}

func (d *RegistryDispatcher) Dispatch(task ExecuteTask) (Executor, error) {
	if d == nil || d.Registry == nil {
		return nil, fmt.Errorf("executor registry dispatcher is not configured")
	}
	if err := task.Validate(); err != nil {
		return nil, err
	}
	execImpl, ok := d.Registry.Get(Type(task.ExecutorType))
	if !ok {
		return nil, fmt.Errorf("executor not found for type: %s", task.ExecutorType)
	}
	return execImpl, nil
}
