package executor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type QueueBroker interface {
	Enqueue(ctx context.Context, queue string, task ExecuteTask) (string, error)
	Dequeue(ctx context.Context, queue string, wait time.Duration) (*QueuedTask, error)
	Ack(ctx context.Context, ticket string, result ExecuteResult) error
	Poll(ctx context.Context, ticket string) (ExecuteResult, error)
	Cancel(ctx context.Context, ticket string) error
}

type QueuedTask struct {
	Ticket string      `json:"ticket"`
	Queue  string      `json:"queue"`
	Task   ExecuteTask `json:"task"`
}

type QueueExecutor struct {
	Broker QueueBroker
}

func NewQueueExecutor(broker QueueBroker) *QueueExecutor {
	return &QueueExecutor{Broker: broker}
}

func (e *QueueExecutor) Type() Type { return TypeQueue }

func (e *QueueExecutor) Execute(ctx context.Context, task ExecuteTask) (ExecuteResult, error) {
	if e == nil || e.Broker == nil {
		return ExecuteResult{}, fmt.Errorf("queue broker is nil")
	}
	queueName := queueNameFromTask(task)
	queued := targetTask(task)
	ticket, err := e.Broker.Enqueue(ctx, queueName, queued)
	if err != nil {
		return ExecuteResult{}, err
	}
	return ExecuteResult{
		Status:         StatusAccepted,
		ExternalTaskID: ticket,
		Metadata: map[string]any{
			"queue": queueName,
		},
	}, nil
}

func (e *QueueExecutor) Poll(ctx context.Context, task ExecuteTask, externalTaskID string) (ExecuteResult, error) {
	if e == nil || e.Broker == nil {
		return ExecuteResult{}, fmt.Errorf("queue broker is nil")
	}
	return e.Broker.Poll(ctx, externalTaskID)
}

func (e *QueueExecutor) Cancel(ctx context.Context, task ExecuteTask, externalTaskID string) error {
	if e == nil || e.Broker == nil {
		return fmt.Errorf("queue broker is nil")
	}
	return e.Broker.Cancel(ctx, externalTaskID)
}

type InMemoryQueueBroker struct {
	mu     sync.RWMutex
	queues map[string]chan QueuedTask
	jobs   map[string]*queueJob
}

type queueJob struct {
	queue  string
	task   ExecuteTask
	result ExecuteResult
	done   bool
}

func NewInMemoryQueueBroker() *InMemoryQueueBroker {
	return &InMemoryQueueBroker{
		queues: make(map[string]chan QueuedTask),
		jobs:   make(map[string]*queueJob),
	}
}

func (b *InMemoryQueueBroker) Enqueue(ctx context.Context, queue string, task ExecuteTask) (string, error) {
	if queue == "" {
		queue = "default"
	}
	ticket := uuid.NewString()
	job := &queueJob{
		queue: queue,
		task:  task,
		result: ExecuteResult{
			Status: StatusRunning,
		},
	}

	b.mu.Lock()
	ch := b.queueChannel(queue)
	b.jobs[ticket] = job
	b.mu.Unlock()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case ch <- QueuedTask{Ticket: ticket, Queue: queue, Task: task}:
		return ticket, nil
	}
}

func (b *InMemoryQueueBroker) Dequeue(ctx context.Context, queue string, wait time.Duration) (*QueuedTask, error) {
	if queue == "" {
		queue = "default"
	}
	b.mu.Lock()
	ch := b.queueChannel(queue)
	b.mu.Unlock()

	if wait <= 0 {
		wait = 50 * time.Millisecond
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, nil
		case task := <-ch:
			b.mu.RLock()
			job := b.jobs[task.Ticket]
			b.mu.RUnlock()
			if job == nil {
				continue
			}
			b.mu.Lock()
			if job.done && job.result.Error == "cancelled" {
				b.mu.Unlock()
				continue
			}
			job.result.Status = StatusRunning
			b.mu.Unlock()
			return &task, nil
		}
	}
}

func (b *InMemoryQueueBroker) Ack(_ context.Context, ticket string, result ExecuteResult) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	job := b.jobs[ticket]
	if job == nil {
		return fmt.Errorf("queue ticket not found: %s", ticket)
	}
	job.result = result
	job.done = true
	return nil
}

func (b *InMemoryQueueBroker) Poll(_ context.Context, ticket string) (ExecuteResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	job := b.jobs[ticket]
	if job == nil {
		return ExecuteResult{}, fmt.Errorf("queue ticket not found: %s", ticket)
	}
	if job.done {
		return job.result, nil
	}
	return ExecuteResult{Status: StatusRunning, ExternalTaskID: ticket}, nil
}

func (b *InMemoryQueueBroker) Cancel(_ context.Context, ticket string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	job := b.jobs[ticket]
	if job == nil {
		return fmt.Errorf("queue ticket not found: %s", ticket)
	}
	job.done = true
	job.result = ExecuteResult{
		Status: StatusFailed,
		Error:  "cancelled",
	}
	return nil
}

func (b *InMemoryQueueBroker) queueChannel(queue string) chan QueuedTask {
	ch := b.queues[queue]
	if ch == nil {
		ch = make(chan QueuedTask, 128)
		b.queues[queue] = ch
	}
	return ch
}

func queueNameFromTask(task ExecuteTask) string {
	if task.ExecutorRef != "" {
		return task.ExecutorRef
	}
	if task.Params != nil {
		if queue, ok := task.Params["queue"].(string); ok && queue != "" {
			return queue
		}
	}
	return "default"
}

func targetTask(task ExecuteTask) ExecuteTask {
	cloned := task
	if task.Params == nil {
		return cloned
	}
	params := make(map[string]any, len(task.Params))
	for key, value := range task.Params {
		params[key] = value
	}
	if targetType, ok := params["target_executor_type"].(string); ok && targetType != "" {
		cloned.ExecutorType = targetType
		delete(params, "target_executor_type")
	}
	if targetRef, ok := params["target_executor_ref"].(string); ok && targetRef != "" {
		cloned.ExecutorRef = targetRef
		delete(params, "target_executor_ref")
	}
	delete(params, "queue")
	cloned.Params = params
	return cloned
}
