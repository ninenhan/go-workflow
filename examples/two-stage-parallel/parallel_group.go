package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

// UnitCall describes one child unit invocation inside a workflow node.
type UnitCall struct {
	ID     string         `json:"id"`
	Ref    string         `json:"ref"`
	Input  any            `json:"input,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

// ParallelGroupUnit runs child units with a bounded worker pool. The registry
// is injected by the host application and is deliberately excluded from JSON
// hydration; Units and MaxConcurrency come from the workflow node Params.
type ParallelGroupUnit struct {
	workerunit.Unit
	Units          []UnitCall `json:"units"`
	MaxConcurrency int        `json:"max_concurrency"`

	registry *workerunit.Registry
}

func (u *ParallelGroupUnit) GetUnitName() string { return "ParallelGroupUnit" }

func (u *ParallelGroupUnit) GetUnitMeta() *workerunit.Unit { return &u.Unit }

func (u *ParallelGroupUnit) Execute(
	ctx context.Context,
	state workerunit.ContextMap,
	_ *workerunit.Node,
) (*workerunit.ExecutionResult, error) {
	if u.registry == nil {
		return nil, errors.New("ParallelGroupUnit: unit registry is required")
	}
	if len(u.Units) == 0 {
		return nil, errors.New("ParallelGroupUnit: units must not be empty")
	}
	if u.MaxConcurrency < 1 {
		return nil, errors.New("ParallelGroupUnit: max_concurrency must be greater than zero")
	}
	seen := make(map[string]struct{}, len(u.Units))
	for _, call := range u.Units {
		if call.ID == "" {
			return nil, errors.New("ParallelGroupUnit: child unit id is required")
		}
		if call.Ref == "" {
			return nil, fmt.Errorf("ParallelGroupUnit: child unit %s ref is required", call.ID)
		}
		if _, exists := seen[call.ID]; exists {
			return nil, fmt.Errorf("ParallelGroupUnit: child unit id is duplicated: %s", call.ID)
		}
		seen[call.ID] = struct{}{}
	}

	results := make([]*workerunit.ExecutionResult, len(u.Units))
	errs := make([]error, len(u.Units))
	jobs := make(chan int, len(u.Units))
	for index := range u.Units {
		jobs <- index
	}
	close(jobs)

	workerCount := min(u.MaxConcurrency, len(u.Units))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				if err := ctx.Err(); err != nil {
					errs[index] = err
					continue
				}
				results[index], errs[index] = u.executeChild(ctx, state, u.Units[index])
			}
		}()
	}
	workers.Wait()

	output := make(map[string]any, len(u.Units))
	var executionErrors []error
	for index, call := range u.Units {
		if errs[index] != nil {
			executionErrors = append(executionErrors, fmt.Errorf("unit %s: %w", call.ID, errs[index]))
			continue
		}
		output[call.ID] = results[index].Data
	}
	if len(executionErrors) > 0 {
		return nil, errors.Join(executionErrors...)
	}
	return workerunit.SimpleResult(output), nil
}

func (u *ParallelGroupUnit) executeChild(
	ctx context.Context,
	state workerunit.ContextMap,
	call UnitCall,
) (*workerunit.ExecutionResult, error) {
	if call.ID == "" {
		return nil, errors.New("id is required")
	}
	if call.Ref == "" {
		return nil, errors.New("ref is required")
	}
	child, ok := u.registry.New(call.Ref)
	if !ok {
		return nil, fmt.Errorf("unit is not registered: %s", call.Ref)
	}
	if err := hydrateChild(child, call.Params); err != nil {
		return nil, fmt.Errorf("hydrate %s: %w", call.Ref, err)
	}
	result, err := child.Execute(ctx, cloneState(state), &workerunit.Node{
		ID:     call.ID,
		Name:   call.ID,
		Input:  &workerunit.Input{Data: call.Input},
		Params: cloneMap(call.Params),
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return workerunit.SimpleResult(nil), nil
	}
	if len(result.Variables) > 0 || len(result.DeleteVariables) > 0 || result.Stream ||
		result.Raw != nil || result.Error != "" || result.Control != "" || result.ControlTarget != "" {
		return nil, errors.New("child unit returned side effects that ParallelGroupUnit cannot aggregate")
	}
	return result, nil
}

func hydrateChild(child workerunit.ExecutableUnit, params map[string]any) error {
	if len(params) == 0 {
		return nil
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, child)
}

func cloneState(state workerunit.ContextMap) workerunit.ContextMap {
	cloned := make(workerunit.ContextMap, len(state))
	for key, value := range state {
		if value == nil {
			cloned[key] = nil
			continue
		}
		copyOfValue := *value
		cloned[key] = &copyOfValue
	}
	return cloned
}

func cloneMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
