package scheduler

import (
	"context"
	"fmt"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

type DispatchMode string

const (
	DispatchLocalFirst  DispatchMode = "local_first"
	DispatchRemoteFirst DispatchMode = "remote_first"
	DispatchRemoteOnly  DispatchMode = "remote_only"
)

type HybridDispatcher struct {
	LocalRegistry *executor.Registry
	Workers       WorkerRegistry
	HTTPClient    *RemoteHTTPClient
	Mode          DispatchMode
}

func NewHybridDispatcher(local *executor.Registry, workers WorkerRegistry, client *RemoteHTTPClient) *HybridDispatcher {
	if local == nil {
		local = executor.NewRegistry()
	}
	if client == nil {
		client = NewRemoteHTTPClient(nil)
	}
	return &HybridDispatcher{
		LocalRegistry: local,
		Workers:       workers,
		HTTPClient:    client,
		Mode:          DispatchLocalFirst,
	}
}

func (d *HybridDispatcher) Dispatch(task executor.ExecuteTask) (executor.Executor, error) {
	if err := task.Validate(); err != nil {
		return nil, err
	}
	switch d.Mode {
	case DispatchRemoteOnly:
		return d.remote(task)
	case DispatchRemoteFirst:
		if execImpl, err := d.remote(task); err == nil {
			return execImpl, nil
		}
		return d.local(task)
	default:
		if execImpl, err := d.local(task); err == nil {
			return execImpl, nil
		}
		return d.remote(task)
	}
}

func (d *HybridDispatcher) local(task executor.ExecuteTask) (executor.Executor, error) {
	if d == nil || d.LocalRegistry == nil {
		return nil, fmt.Errorf("local executor registry is not configured")
	}
	execImpl, ok := d.LocalRegistry.Get(executor.Type(task.ExecutorType))
	if !ok {
		return nil, fmt.Errorf("local executor not found for type=%s", task.ExecutorType)
	}
	return execImpl, nil
}

func (d *HybridDispatcher) remote(task executor.ExecuteTask) (executor.Executor, error) {
	if d == nil || d.Workers == nil {
		return nil, fmt.Errorf("worker registry is not configured")
	}
	if allocator, ok := d.Workers.(WorkerAllocator); ok {
		lease, err := allocator.AcquireForTask(context.Background(), task)
		if err != nil {
			return nil, err
		}
		return &RemoteBoundExecutor{
			worker:  lease.Worker,
			client:  d.HTTPClient,
			release: lease.Release,
		}, nil
	}
	worker, err := d.Workers.FindForTask(context.Background(), task)
	if err != nil {
		return nil, err
	}
	return &RemoteBoundExecutor{
		worker: worker,
		client: d.HTTPClient,
	}, nil
}

type RemoteBoundExecutor struct {
	worker   *workerproto.WorkerDescriptor
	client   *RemoteHTTPClient
	release  func()
	released bool
}

func (e *RemoteBoundExecutor) Type() executor.Type {
	return executor.TypeRemote
}

func (e *RemoteBoundExecutor) Execute(ctx context.Context, task executor.ExecuteTask) (executor.ExecuteResult, error) {
	result, err := e.client.Execute(ctx, *e.worker, task)
	if err != nil {
		e.releaseOnce()
		return result, err
	}
	status := result.NormalizedStatus()
	if status == executor.StatusSucceeded || status == executor.StatusFailed || status == executor.StatusRetryable {
		e.releaseOnce()
	}
	return result, nil
}

func (e *RemoteBoundExecutor) Poll(ctx context.Context, task executor.ExecuteTask, externalTaskID string) (executor.ExecuteResult, error) {
	result, err := e.client.Poll(ctx, *e.worker, task, externalTaskID)
	if err != nil {
		e.releaseOnce()
		return result, err
	}
	status := result.NormalizedStatus()
	if status == executor.StatusSucceeded || status == executor.StatusFailed || status == executor.StatusRetryable {
		e.releaseOnce()
	}
	return result, nil
}

func (e *RemoteBoundExecutor) Cancel(ctx context.Context, task executor.ExecuteTask, externalTaskID string) error {
	err := e.client.Cancel(ctx, *e.worker, task, externalTaskID)
	e.releaseOnce()
	return err
}

func (e *RemoteBoundExecutor) releaseOnce() {
	if e == nil || e.released || e.release == nil {
		return
	}
	e.release()
	e.released = true
}

var _ executor.AsyncExecutor = (*RemoteBoundExecutor)(nil)
