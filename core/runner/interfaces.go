package runner

import (
	"context"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/planning"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
)

// NodeDispatcher decides which nodes are ready based on execution state.
type NodeDispatcher interface {
	Dispatch(plan *planning.ExecutionPlan, run *wfruntime.WorkflowRun) []string
}

// Backward compatible alias.
type Dispatcher = NodeDispatcher

type Scheduler interface {
	Run(ctx context.Context, plan *planning.ExecutionPlan, run *wfruntime.WorkflowRun) (*wfruntime.WorkflowRun, error)
}

type EventSink interface {
	Emit(ctx context.Context, event wfruntime.RunEvent)
}

// ResultReporter persists/forwards normalized task results.
type ResultReporter interface {
	ReportResult(ctx context.Context, task executor.ExecuteTask, result executor.ExecuteResult) error
}

type Heartbeat struct {
	RunID          string          `json:"run_id"`
	NodeID         string          `json:"node_id"`
	ExecutorType   string          `json:"executor_type,omitempty"`
	ExternalTaskID string          `json:"external_task_id,omitempty"`
	Status         executor.Status `json:"status,omitempty"`
	Message        string          `json:"message,omitempty"`
	At             time.Time       `json:"at"`
	Metadata       map[string]any  `json:"metadata,omitempty"`
}

// HeartbeatReporter is useful for async/remote execution visibility.
type HeartbeatReporter interface {
	ReportHeartbeat(ctx context.Context, beat Heartbeat) error
}

// ExecutorResolver keeps compatibility with existing call sites.
type ExecutorResolver interface {
	Resolve(execType string) (executor.Executor, bool)
}
