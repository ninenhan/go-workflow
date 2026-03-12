package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// ScriptExecutor executes lightweight inline scripts for python/node runtimes.
// It is intentionally simple and can be replaced by a sandboxed implementation later.
type ScriptExecutor struct{}

func NewScriptExecutor() *ScriptExecutor {
	return &ScriptExecutor{}
}

func (e *ScriptExecutor) Type() Type { return TypeScript }

func (e *ScriptExecutor) Execute(ctx context.Context, task ExecuteTask) (ExecuteResult, error) {
	runtimeName, _ := task.Params["runtime"].(string)
	script, _ := task.Params["script"].(string)
	if runtimeName == "" || script == "" {
		return ExecuteResult{}, fmt.Errorf("script executor requires params.runtime and params.script")
	}

	var cmd *exec.Cmd
	switch runtimeName {
	case "python", "python3":
		cmd = exec.CommandContext(ctx, "python3", "-c", script)
	case "node", "nodejs":
		cmd = exec.CommandContext(ctx, "node", "-e", script)
	default:
		return ExecuteResult{}, fmt.Errorf("unsupported script runtime: %s", runtimeName)
	}

	if task.Input != nil {
		buf, err := json.Marshal(task.Input)
		if err != nil {
			return ExecuteResult{}, fmt.Errorf("marshal task input: %w", err)
		}
		cmd.Stdin = bytes.NewReader(buf)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return ExecuteResult{
			Status: StatusFailed,
			Error:  err.Error(),
			Metadata: map[string]any{
				"runtime": runtimeName,
			},
			Logs: []string{string(output)},
		}, err
	}

	var parsed any
	if err := json.Unmarshal(output, &parsed); err != nil {
		parsed = string(output)
	}

	return ExecuteResult{
		Status: StatusSucceeded,
		Output: parsed,
		Metadata: map[string]any{
			"runtime": runtimeName,
		},
		Logs: []string{string(output)},
	}, nil
}
