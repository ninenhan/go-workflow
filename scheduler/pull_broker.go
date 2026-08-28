package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

const completedCommandCacheLimit = 4096

type pullCommandState struct {
	workerID  string
	command   workerproto.Command
	leaseEnds time.Time
	response  workerproto.CompleteRequest
	done      chan struct{}
	completed bool
}

// PullBroker provides an outbound-only worker transport. Commands are leased,
// not removed, by Pull so a worker that disconnects before receiving the HTTP
// response gets the same command again after LeaseDuration.
type PullBroker struct {
	mu             sync.Mutex
	commands       map[string]*pullCommandState
	order          []string
	completed      map[string]string
	completedOrder []string
	notify         chan struct{}
	LeaseDuration  time.Duration
	WaitDuration   time.Duration
	Now            func() time.Time
}

func NewPullBroker() *PullBroker {
	return &PullBroker{
		commands:      make(map[string]*pullCommandState),
		completed:     make(map[string]string),
		notify:        make(chan struct{}),
		LeaseDuration: 15 * time.Second,
		WaitDuration:  20 * time.Second,
		Now:           time.Now,
	}
}

func (b *PullBroker) Dispatch(ctx context.Context, workerID string, operation workerproto.Operation, task executor.ExecuteTask, externalTaskID string) (workerproto.CompleteRequest, error) {
	if b == nil {
		return workerproto.CompleteRequest{}, fmt.Errorf("pull broker is not configured")
	}
	if workerID == "" {
		return workerproto.CompleteRequest{}, fmt.Errorf("worker id is required")
	}
	state := &pullCommandState{
		workerID: workerID,
		command: workerproto.Command{
			CommandID:      uuid.NewString(),
			Operation:      operation,
			Task:           task,
			ExternalTaskID: externalTaskID,
		},
		done: make(chan struct{}),
	}
	b.mu.Lock()
	b.commands[state.command.CommandID] = state
	b.order = append(b.order, state.command.CommandID)
	b.signalLocked()
	b.mu.Unlock()

	select {
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.commands, state.command.CommandID)
		b.compactOrderLocked()
		b.mu.Unlock()
		return workerproto.CompleteRequest{}, ctx.Err()
	case <-state.done:
		b.mu.Lock()
		delete(b.commands, state.command.CommandID)
		b.compactOrderLocked()
		response := state.response
		b.mu.Unlock()
		return response, nil
	}
}

func (b *PullBroker) Pull(ctx context.Context, workerID string) (*workerproto.Command, error) {
	if b == nil {
		return nil, fmt.Errorf("pull broker is not configured")
	}
	if workerID == "" {
		return nil, fmt.Errorf("worker id is required")
	}
	wait := b.WaitDuration
	if wait <= 0 {
		wait = 20 * time.Second
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	for {
		b.mu.Lock()
		now := b.now()
		for _, commandID := range b.order {
			state := b.commands[commandID]
			if state == nil || state.completed || state.workerID != workerID || now.Before(state.leaseEnds) {
				continue
			}
			lease := b.LeaseDuration
			if lease <= 0 {
				lease = 15 * time.Second
			}
			state.leaseEnds = now.Add(lease)
			state.command.Delivery++
			command := state.command
			b.mu.Unlock()
			return &command, nil
		}
		notify := b.notify
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, nil
		case <-notify:
		}
	}
}

func (b *PullBroker) Complete(req workerproto.CompleteRequest) error {
	if b == nil {
		return fmt.Errorf("pull broker is not configured")
	}
	if req.WorkerID == "" || req.CommandID == "" {
		return fmt.Errorf("worker_id and command_id are required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.commands[req.CommandID]
	if state == nil {
		if b.completed[req.CommandID] == req.WorkerID {
			return nil
		}
		return fmt.Errorf("pull command not found: %s", req.CommandID)
	}
	if state.workerID != req.WorkerID {
		return fmt.Errorf("pull command belongs to worker %s", state.workerID)
	}
	if state.completed {
		return nil
	}
	switch state.command.Operation {
	case workerproto.OperationExecute, workerproto.OperationPoll:
		if req.Result == nil && req.Error == "" {
			return fmt.Errorf("pull command %s completion requires result or error", req.CommandID)
		}
	case workerproto.OperationCancel:
		if !req.Cancelled && req.Error == "" {
			return fmt.Errorf("pull command %s cancellation requires cancelled or error", req.CommandID)
		}
	}
	state.response = req
	state.completed = true
	b.completed[req.CommandID] = req.WorkerID
	b.completedOrder = append(b.completedOrder, req.CommandID)
	for len(b.completedOrder) > completedCommandCacheLimit {
		oldest := b.completedOrder[0]
		b.completedOrder[0] = ""
		b.completedOrder = b.completedOrder[1:]
		delete(b.completed, oldest)
	}
	close(state.done)
	return nil
}

func (b *PullBroker) signalLocked() {
	close(b.notify)
	b.notify = make(chan struct{})
}

func (b *PullBroker) compactOrderLocked() {
	if len(b.order) <= len(b.commands)*2+128 {
		return
	}
	order := make([]string, 0, len(b.commands))
	for _, commandID := range b.order {
		if b.commands[commandID] != nil {
			order = append(order, commandID)
		}
	}
	b.order = order
}

func (b *PullBroker) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}
