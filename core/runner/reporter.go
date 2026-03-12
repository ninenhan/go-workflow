package runner

import (
	"context"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
)

type NopResultReporter struct{}

func (r *NopResultReporter) ReportResult(context.Context, executor.ExecuteTask, executor.ExecuteResult) error {
	return nil
}

type NopHeartbeatReporter struct{}

func (r *NopHeartbeatReporter) ReportHeartbeat(context.Context, Heartbeat) error {
	return nil
}

// StoreResultReporter forwards normalized results into runtime events.
type StoreResultReporter struct {
	Store wfruntime.Store
}

func (r *StoreResultReporter) ReportResult(ctx context.Context, task executor.ExecuteTask, result executor.ExecuteResult) error {
	if r == nil || r.Store == nil {
		return nil
	}
	status := mapResultStatus(result.NormalizedStatus())
	return r.Store.AppendEvent(ctx, wfruntime.RunEvent{
		RunID:   task.RunID,
		NodeID:  task.NodeID,
		Type:    wfruntime.EventNodeDone,
		Status:  status,
		Time:    time.Now(),
		Message: result.Error,
		Payload: map[string]any{
			"executor_type":    task.ExecutorType,
			"executor_ref":     task.ExecutorRef,
			"external_task_id": result.ExternalTaskID,
			"metadata":         result.Metadata,
		},
	})
}

func mapResultStatus(s executor.Status) wfruntime.Status {
	switch s {
	case executor.StatusSucceeded:
		return wfruntime.StatusSuccess
	case executor.StatusRetryable:
		return wfruntime.StatusRetry
	case executor.StatusAccepted, executor.StatusRunning:
		return wfruntime.StatusRunning
	case executor.StatusFailed:
		return wfruntime.StatusFailed
	default:
		return wfruntime.StatusRunning
	}
}
