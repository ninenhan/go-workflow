package worker

import (
	"context"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
)

func (s *Service) ConsumeQueue(ctx context.Context, broker executor.QueueBroker, queue string, wait time.Duration) error {
	if s == nil || !s.enabled {
		return executor.ErrNotImplemented
	}
	if broker == nil {
		return executor.ErrNotImplemented
	}
	if wait <= 0 {
		wait = 50 * time.Millisecond
	}
	for {
		task, err := broker.Dequeue(ctx, queue, wait)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if task == nil {
			continue
		}
		result, err := s.executeTask(ctx, task.Task)
		if err != nil {
			_ = broker.Ack(ctx, task.Ticket, executor.ExecuteResult{
				Status: executor.StatusFailed,
				Error:  err.Error(),
			})
			continue
		}
		_ = broker.Ack(ctx, task.Ticket, result)
	}
}
