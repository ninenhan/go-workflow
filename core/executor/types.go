package executor

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Type string

const (
	TypeHTTP      Type = "http"
	TypeLocalGo   Type = "local_go"
	TypeScript    Type = "script"
	TypePython    Type = "python"
	TypeNodeJS    Type = "node"
	TypeQueue     Type = "queue"
	TypeRemote    Type = "remote"
	TypeContainer Type = "container"
	TypeUnit      Type = "unit"
)

var ErrNotImplemented = errors.New("executor not implemented")

type Status string

const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusRetryable Status = "retryable"
	StatusAccepted  Status = "accepted" // async task accepted by backend worker
	StatusRunning   Status = "running"  // async task still running when polled
)

type ExecuteTask struct {
	RunID           string         `json:"run_id"`
	NodeID          string         `json:"node_id"`
	ExecutorType    string         `json:"executor_type"`
	ExecutorRef     string         `json:"executor_ref,omitempty"`
	CredentialScope string         `json:"credential_scope,omitempty"`
	Attempt         int            `json:"attempt,omitempty"`
	MaxAttempts     int            `json:"max_attempts,omitempty"`
	Input           any            `json:"input,omitempty"`
	Params          map[string]any `json:"params,omitempty"`
	Context         map[string]any `json:"context,omitempty"`
	Timeout         time.Duration  `json:"timeout,omitempty"`
	Deadline        time.Time      `json:"deadline,omitempty"`
	Async           bool           `json:"async,omitempty"`
	PollInterval    time.Duration  `json:"poll_interval,omitempty"`
	HeartbeatFreq   time.Duration  `json:"heartbeat_freq,omitempty"`
}

type ExecuteResult struct {
	Status          Status         `json:"status,omitempty"`
	Output          any            `json:"output,omitempty"`
	Variables       map[string]any `json:"variables,omitempty"`
	DeleteVariables []string       `json:"delete_variables,omitempty"`
	Error           string         `json:"error,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Logs            []string       `json:"logs,omitempty"`
	ExternalTaskID  string         `json:"external_task_id,omitempty"`
	RetryAfter      time.Duration  `json:"retry_after,omitempty"`
	FinishedAt      time.Time      `json:"finished_at,omitempty"`
}

// ExecutionFailure lets executors distinguish permanent failures from
// transient failures without making the scheduler inspect error strings.
type ExecutionFailure struct {
	Err        error
	Retry      bool
	RetryAfter time.Duration
}

func (e *ExecutionFailure) Error() string {
	if e == nil || e.Err == nil {
		return "execution failed"
	}
	return e.Err.Error()
}

func (e *ExecutionFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func PermanentFailure(err error) error {
	if err == nil {
		return nil
	}
	return &ExecutionFailure{Err: err}
}

func RetryableFailure(err error, retryAfter time.Duration) error {
	if err == nil {
		return nil
	}
	return &ExecutionFailure{Err: err, Retry: true, RetryAfter: retryAfter}
}

func ClassifyFailure(err error) (retryable bool, retryAfter time.Duration, classified bool) {
	var failure *ExecutionFailure
	if !errors.As(err, &failure) {
		return false, 0, false
	}
	return failure.Retry, failure.RetryAfter, true
}

func (r ExecuteResult) NormalizedStatus() Status {
	if r.Status != "" {
		return r.Status
	}
	if r.Error != "" {
		return StatusFailed
	}
	return StatusSucceeded
}

func (t ExecuteTask) Validate() error {
	if t.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if t.NodeID == "" {
		return fmt.Errorf("node_id is required")
	}
	if t.ExecutorType == "" {
		return fmt.Errorf("executor_type is required")
	}
	return nil
}

type Executor interface {
	Type() Type
	Execute(ctx context.Context, task ExecuteTask) (ExecuteResult, error)
}

// AsyncExecutor is optional and only needed for queue/worker style execution.
type AsyncExecutor interface {
	Executor
	Poll(ctx context.Context, task ExecuteTask, externalTaskID string) (ExecuteResult, error)
	Cancel(ctx context.Context, task ExecuteTask, externalTaskID string) error
}

// Dispatcher resolves a task to a concrete executor implementation.
type Dispatcher interface {
	Dispatch(task ExecuteTask) (Executor, error)
}

// Backward-compatible aliases for existing code during migration.
type Request = ExecuteTask

type Result = ExecuteResult
