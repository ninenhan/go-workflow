package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

func TestPullBrokerRedeliversLeaseAndAcceptsDuplicateCompletion(t *testing.T) {
	broker := NewPullBroker()
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	broker.Now = func() time.Time { return now }
	broker.LeaseDuration = time.Second
	broker.WaitDuration = 100 * time.Millisecond

	type dispatchOutcome struct {
		response workerproto.CompleteRequest
		err      error
	}
	dispatched := make(chan dispatchOutcome, 1)
	go func() {
		response, err := broker.Dispatch(context.Background(), "worker-1", workerproto.OperationExecute, executor.ExecuteTask{
			DispatchID:   "dispatch-1",
			RunID:        "run-1",
			NodeID:       "node-1",
			ExecutorType: "unit",
		}, "")
		dispatched <- dispatchOutcome{response: response, err: err}
	}()

	first, err := broker.Pull(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("first pull: %v", err)
	}
	if first == nil || first.Delivery != 1 {
		t.Fatalf("unexpected first delivery: %+v", first)
	}

	now = now.Add(2 * time.Second)
	second, err := broker.Pull(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if second == nil || second.CommandID != first.CommandID || second.Delivery != 2 {
		t.Fatalf("unexpected redelivery: first=%+v second=%+v", first, second)
	}

	result := executor.ExecuteResult{Status: executor.StatusSucceeded, Output: "ok"}
	completion := workerproto.CompleteRequest{
		WorkerID:  "worker-1",
		CommandID: first.CommandID,
		Result:    &result,
	}
	if err := broker.Complete(completion); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := broker.Complete(completion); err != nil {
		t.Fatalf("duplicate complete: %v", err)
	}

	outcome := <-dispatched
	if outcome.err != nil || outcome.response.Result == nil || outcome.response.Result.Output != "ok" {
		t.Fatalf("unexpected dispatch outcome: %+v err=%v", outcome.response, outcome.err)
	}
}
