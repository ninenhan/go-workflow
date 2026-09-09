package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/core/executor"
)

type Executor struct {
	Registry    *Registry
	Credentials credential.Resolver
}

func NewExecutor(reg *Registry) *Executor {
	return NewExecutorWithCredentials(reg, credential.EnvironmentResolver{})
}

func NewExecutorWithCredentials(reg *Registry, credentials credential.Resolver) *Executor {
	if reg == nil {
		reg = DefaultRegistry
	}
	return &Executor{Registry: reg, Credentials: credentials}
}

func (e *Executor) Type() executor.Type {
	return executor.TypeUnit
}

func (e *Executor) Execute(ctx context.Context, task executor.ExecuteTask) (executor.ExecuteResult, error) {
	if e == nil || e.Registry == nil {
		return executor.ExecuteResult{}, fmt.Errorf("unit registry is nil")
	}
	unitName := findUnitName(task)
	if unitName == "" {
		return executor.ExecuteResult{}, fmt.Errorf("unit executor missing executor_ref")
	}

	execImpl, ok := e.Registry.New(unitName)
	if !ok {
		return executor.ExecuteResult{}, fmt.Errorf("unit not registered: %s", unitName)
	}
	if err := hydrate(execImpl, task.Params); err != nil {
		return executor.ExecuteResult{}, fmt.Errorf("hydrate unit %s: %w", unitName, err)
	}
	if e.Credentials == nil {
		return executor.ExecuteResult{}, fmt.Errorf("unit credential resolver is not configured")
	}
	executionContext, err := credential.WithResolver(ctx, e.Credentials, task.CredentialScope)
	if err != nil {
		return executor.ExecuteResult{}, fmt.Errorf("prepare unit credentials: %w", err)
	}
	executionContext = withDispatchID(executionContext, task.DispatchID)

	res, err := execImpl.Execute(executionContext, buildContext(task.Context), &Node{
		ID:     task.NodeID,
		Input:  &Input{Data: task.Input},
		Params: cloneParams(task.Params),
	})
	if err != nil {
		if retryable, retryAfter, classified := executor.ClassifyFailure(err); classified {
			status := executor.StatusFailed
			if retryable {
				status = executor.StatusRetryable
			}
			return executor.ExecuteResult{
				Status:     status,
				Error:      err.Error(),
				RetryAfter: retryAfter,
			}, nil
		}
		return executor.ExecuteResult{}, err
	}
	if res == nil {
		return executor.ExecuteResult{Status: executor.StatusSucceeded}, nil
	}
	status := res.Status
	switch status {
	case "":
		status = executor.StatusSucceeded
	case executor.StatusSucceeded, executor.StatusFailed, executor.StatusRetryable:
	case executor.StatusAccepted, executor.StatusRunning:
		if strings.TrimSpace(res.ExternalTaskID) == "" {
			return executor.ExecuteResult{}, fmt.Errorf("unit %s async result missing external_task_id", unitName)
		}
	default:
		return executor.ExecuteResult{}, fmt.Errorf("unit %s returned unsupported status: %s", unitName, status)
	}

	metadata := map[string]any{
		"node_name": res.NodeName,
		"stream":    res.Stream,
		"raw":       res.Raw,
		"error":     res.Error,
	}
	if res.Control != "" {
		metadata["control"] = string(res.Control)
	}
	if res.ControlTarget != "" {
		metadata["control_target"] = res.ControlTarget
	}

	return executor.ExecuteResult{
		AwaitCallback:   status == executor.StatusAccepted || status == executor.StatusRunning,
		TTL:             res.TTL,
		ExpireAt:        res.ExpireAt,
		Status:          status,
		ExternalTaskID:  res.ExternalTaskID,
		Error:           res.Error,
		Output:          res.Data,
		Variables:       cloneParams(res.Variables),
		DeleteVariables: append([]string(nil), res.DeleteVariables...),
		Metadata:        metadata,
	}, nil
}

func buildContext(src map[string]any) ContextMap {
	if len(src) == 0 {
		return ContextMap{}
	}
	out := make(ContextMap, len(src))
	for key, value := range src {
		out[key] = &ExecutionResult{Data: value}
	}
	return out
}

func hydrate(execImpl Executable, params map[string]any) error {
	if len(params) == 0 || execImpl == nil {
		return nil
	}
	buf, err := json.Marshal(stripInternalParams(params))
	if err != nil {
		return err
	}
	return json.Unmarshal(buf, execImpl)
}

func stripInternalParams(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		if strings.HasPrefix(key, "__") {
			continue
		}
		dst[key] = value
	}
	return dst
}

func cloneParams(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func findUnitName(task executor.ExecuteTask) string {
	if task.ExecutorRef != "" {
		return task.ExecutorRef
	}
	if task.Params == nil {
		return ""
	}
	if value, ok := task.Params["__executor_ref"].(string); ok && value != "" {
		return value
	}
	if value, ok := task.Params["unit"].(string); ok && value != "" {
		return value
	}
	if value, ok := task.Params["unit_id"].(string); ok && value != "" {
		return value
	}
	return ""
}
