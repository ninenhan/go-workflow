package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

type PullOptions struct {
	SchedulerEndpoint string
	Descriptor        workerproto.WorkerDescriptor
	Interval          time.Duration
	RetryInterval     time.Duration
	Concurrency       int
	Client            *RegistrationClient
}

// MaintainPull registers an outbound-only worker, leases commands from the
// scheduler, and retries completion with the same command_id until acknowledged.
func (s *Service) MaintainPull(ctx context.Context, opts PullOptions) error {
	if s == nil || !s.Enabled() {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("pull context is required")
	}
	client := opts.Client
	if client == nil {
		client = NewRegistrationClient(nil)
	}
	descriptor := s.Descriptor(opts.Descriptor)
	descriptor.Transport = workerproto.TransportPull
	descriptor.Endpoint = ""
	if err := client.Register(ctx, opts.SchedulerEndpoint, descriptor); err != nil {
		return err
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	retryInterval := opts.RetryInterval
	if retryInterval <= 0 {
		retryInterval = 500 * time.Millisecond
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if descriptor.MaxConcurrent > 0 && concurrency > descriptor.MaxConcurrent {
		concurrency = descriptor.MaxConcurrent
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.pullLoop(workerCtx, client, opts.SchedulerEndpoint, descriptor.ID, retryInterval)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.pullHeartbeatLoop(workerCtx, client, opts.SchedulerEndpoint, descriptor, interval)
	}()

	<-ctx.Done()
	cancel()
	wg.Wait()
	return ctx.Err()
}

func (s *Service) pullLoop(ctx context.Context, client *RegistrationClient, endpoint, workerID string, retryInterval time.Duration) {
	for ctx.Err() == nil {
		command, err := client.Pull(ctx, endpoint, workerID)
		if err != nil {
			if !waitPullRetry(ctx, retryInterval) {
				return
			}
			continue
		}
		if command == nil {
			continue
		}
		completion := s.executeCommand(ctx, workerID, command)
		accepted := false
		for ctx.Err() == nil {
			err := client.Complete(ctx, endpoint, completion)
			if err == nil {
				accepted = true
				break
			}
			var protocolError *ProtocolError
			if errors.As(err, &protocolError) && !protocolError.Response.Retryable {
				break
			}
			if !waitPullRetry(ctx, retryInterval) {
				s.discardPullCommand(command.CommandID)
				return
			}
		}
		if accepted {
			s.acknowledgePullCommand(command.CommandID)
		} else {
			s.discardPullCommand(command.CommandID)
		}
	}
}

func (s *Service) pullHeartbeatLoop(ctx context.Context, client *RegistrationClient, endpoint string, descriptor workerproto.WorkerDescriptor, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := client.Heartbeat(ctx, endpoint, descriptor.ID); err != nil {
				_ = client.Register(ctx, endpoint, descriptor)
			}
		}
	}
}

func (s *Service) executeCommand(parent context.Context, workerID string, command *workerproto.Command) workerproto.CompleteRequest {
	if command == nil || command.CommandID == "" {
		return s.executePullCommand(parent, workerID, command)
	}

	s.pullMu.Lock()
	if _, ok := s.acknowledged[command.CommandID]; ok {
		s.pullMu.Unlock()
		return workerproto.CompleteRequest{WorkerID: workerID, CommandID: command.CommandID}
	}
	if entry := s.pullCommands[command.CommandID]; entry != nil {
		s.pullMu.Unlock()
		select {
		case <-parent.Done():
			return workerproto.CompleteRequest{WorkerID: workerID, CommandID: command.CommandID, Error: parent.Err().Error()}
		case <-entry.done:
			return entry.completion
		}
	}
	entry := &pullCommandEntry{done: make(chan struct{})}
	s.pullCommands[command.CommandID] = entry
	s.pullMu.Unlock()

	completion := s.executePullCommand(parent, workerID, command)
	s.pullMu.Lock()
	entry.completion = completion
	close(entry.done)
	s.pullMu.Unlock()
	return completion
}

func (s *Service) executePullCommand(parent context.Context, workerID string, command *workerproto.Command) workerproto.CompleteRequest {
	if command == nil {
		return workerproto.CompleteRequest{WorkerID: workerID, Error: "pull command is required"}
	}
	completion := workerproto.CompleteRequest{WorkerID: workerID, CommandID: command.CommandID}
	ctx, cancel := commandContext(parent, command.Task)
	defer cancel()

	switch command.Operation {
	case workerproto.OperationExecute:
		result, err := s.executeTask(ctx, command.Task)
		if err != nil {
			var protocolFailure httpErr
			if errors.As(err, &protocolFailure) {
				completion.Error = err.Error()
				return completion
			}
			result = executionErrorResult(err)
		}
		completion.Result = &result
	case workerproto.OperationPoll:
		result, err := s.pollTask(ctx, command.Task, command.ExternalTaskID)
		if err != nil {
			completion.Error = err.Error()
			return completion
		}
		completion.Result = &result
	case workerproto.OperationCancel:
		if err := s.cancelTask(ctx, command.Task, command.ExternalTaskID); err != nil {
			completion.Error = err.Error()
			return completion
		}
		completion.Cancelled = true
	default:
		completion.Error = fmt.Sprintf("unsupported operation: %s", command.Operation)
	}
	return completion
}

func (s *Service) acknowledgePullCommand(commandID string) {
	if commandID == "" {
		return
	}
	s.pullMu.Lock()
	defer s.pullMu.Unlock()
	delete(s.pullCommands, commandID)
	if _, exists := s.acknowledged[commandID]; exists {
		return
	}
	s.acknowledged[commandID] = struct{}{}
	s.ackOrder = append(s.ackOrder, commandID)
	for len(s.ackOrder) > acknowledgedCommandLimit {
		oldest := s.ackOrder[0]
		s.ackOrder[0] = ""
		s.ackOrder = s.ackOrder[1:]
		delete(s.acknowledged, oldest)
	}
}

func (s *Service) discardPullCommand(commandID string) {
	s.pullMu.Lock()
	delete(s.pullCommands, commandID)
	s.pullMu.Unlock()
}

func commandContext(parent context.Context, task executor.ExecuteTask) (context.Context, context.CancelFunc) {
	if !task.Deadline.IsZero() {
		return context.WithDeadline(parent, task.Deadline)
	}
	if task.Timeout > 0 {
		return context.WithTimeout(parent, task.Timeout)
	}
	return context.WithCancel(parent)
}

func waitPullRetry(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
