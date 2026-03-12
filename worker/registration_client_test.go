package worker_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
	"github.com/ninenhan/go-workflow/scheduler"
	"github.com/ninenhan/go-workflow/worker"
)

func TestRegistrationClient_RegisterAndHeartbeat(t *testing.T) {
	workers := scheduler.NewMemoryWorkerRegistry()
	schedulerHTTP := httptest.NewServer(scheduler.NewRegistryHTTPHandler(workers).Handler())
	defer schedulerHTTP.Close()

	client := worker.NewRegistrationClient(nil)
	desc := workerproto.WorkerDescriptor{
		ID:                     "worker-test",
		Endpoint:               "http://worker.local",
		Status:                 workerproto.StatusOnline,
		SupportedExecutorTypes: []string{string(executor.TypeLocalGo)},
		SupportedExecutorRefs:  []string{"echo"},
	}
	if err := client.Register(context.Background(), schedulerHTTP.URL, desc); err != nil {
		t.Fatalf("register worker: %v", err)
	}
	if err := client.Heartbeat(context.Background(), schedulerHTTP.URL, desc.ID); err != nil {
		t.Fatalf("heartbeat worker: %v", err)
	}

	list, err := workers.List(context.Background())
	if err != nil {
		t.Fatalf("list workers: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("unexpected worker count: %d", len(list))
	}
	if list[0].ID != desc.ID {
		t.Fatalf("unexpected worker id: %s", list[0].ID)
	}
}
