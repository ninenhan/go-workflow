package executor

import (
	"context"
	"fmt"
)

type PythonExecutor struct{}

type NodeExecutor struct{}

type RemoteExecutor struct{}

type ContainerExecutor struct{}

func (e *PythonExecutor) Type() Type    { return TypePython }
func (e *NodeExecutor) Type() Type      { return TypeNodeJS }
func (e *RemoteExecutor) Type() Type    { return TypeRemote }
func (e *ContainerExecutor) Type() Type { return TypeContainer }

func (e *PythonExecutor) Execute(context.Context, Request) (Result, error) {
	return Result{}, ErrNotImplemented
}

func (e *NodeExecutor) Execute(context.Context, Request) (Result, error) {
	return Result{}, ErrNotImplemented
}

// RemoteExecutor skeleton: submit -> accepted, then Poll on externalTaskID.
func (e *RemoteExecutor) Execute(ctx context.Context, task Request) (Result, error) {
	externalTaskID := fmt.Sprintf("remote:%s:%s", task.RunID, task.NodeID)
	return Result{
		Status:         StatusAccepted,
		ExternalTaskID: externalTaskID,
		Metadata: map[string]any{
			"mode": "queue",
		},
	}, nil
}

func (e *RemoteExecutor) Poll(context.Context, Request, string) (Result, error) {
	return Result{Status: StatusRunning}, ErrNotImplemented
}

func (e *RemoteExecutor) Cancel(context.Context, Request, string) error {
	return ErrNotImplemented
}

// ContainerExecutor skeleton: submit container job, then poll.
func (e *ContainerExecutor) Execute(ctx context.Context, task Request) (Result, error) {
	externalTaskID := fmt.Sprintf("container:%s:%s", task.RunID, task.NodeID)
	return Result{
		Status:         StatusAccepted,
		ExternalTaskID: externalTaskID,
		Metadata: map[string]any{
			"mode": "job",
		},
	}, nil
}

func (e *ContainerExecutor) Poll(context.Context, Request, string) (Result, error) {
	return Result{Status: StatusRunning}, ErrNotImplemented
}

func (e *ContainerExecutor) Cancel(context.Context, Request, string) error {
	return ErrNotImplemented
}
