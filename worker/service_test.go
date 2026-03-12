package worker_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
	"github.com/ninenhan/go-workflow/scheduler"
	"github.com/ninenhan/go-workflow/worker"
)

func TestNewServiceDisabled(t *testing.T) {
	svc, err := worker.NewService(worker.Options{
		Enabled:          false,
		RegisterBuiltins: true,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if svc.Enabled() {
		t.Fatalf("expected worker to be disabled")
	}
	if err := svc.RegisterExecutor(executor.NewLocalExecutor()); err == nil {
		t.Fatalf("expected register to fail when worker is disabled")
	}
}

func TestNewServiceWithBuiltins(t *testing.T) {
	svc, err := worker.NewService(worker.Options{
		Enabled:          true,
		RegisterBuiltins: true,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if !svc.Enabled() {
		t.Fatalf("expected worker to be enabled")
	}
	if _, ok := svc.Registry().Get(executor.TypeLocalGo); !ok {
		t.Fatalf("missing local executor")
	}
	if _, ok := svc.Registry().Get(executor.TypeHTTP); !ok {
		t.Fatalf("missing http executor")
	}
	if _, ok := svc.Registry().Get(executor.TypeScript); !ok {
		t.Fatalf("missing script executor")
	}
	if _, ok := svc.Registry().Get(executor.TypeUnit); !ok {
		t.Fatalf("missing unit executor")
	}
}

func TestServiceDescriptorIncludesExecutorsAndUnits(t *testing.T) {
	svc, err := worker.NewService(worker.Options{
		Enabled:          true,
		RegisterBuiltins: true,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	desc := svc.Descriptor(workerproto.WorkerDescriptor{
		ID:       "worker-1",
		Endpoint: "http://worker.local",
	})
	if desc.Status != workerproto.StatusOnline {
		t.Fatalf("unexpected status: %s", desc.Status)
	}
	if desc.Weight != 1 {
		t.Fatalf("unexpected weight: %d", desc.Weight)
	}
	if len(desc.SupportedExecutorTypes) == 0 {
		t.Fatalf("expected executor types")
	}
	if len(desc.SupportedExecutorRefs) == 0 {
		t.Fatalf("expected unit refs")
	}
}

func TestServiceMaintainRegistration(t *testing.T) {
	workers := scheduler.NewMemoryWorkerRegistry()
	schedulerHTTP := httptest.NewServer(scheduler.NewRegistryHTTPHandler(workers).Handler())
	defer schedulerHTTP.Close()

	svc, err := worker.NewService(worker.Options{
		Enabled:          true,
		RegisterBuiltins: true,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- svc.MaintainRegistration(ctx, worker.RegistrationOptions{
			SchedulerEndpoint: schedulerHTTP.URL,
			Descriptor: svc.Descriptor(workerproto.WorkerDescriptor{
				ID:       "worker-loop",
				Endpoint: "http://worker.loop",
			}),
			Interval: 20 * time.Millisecond,
		})
	}()

	time.Sleep(60 * time.Millisecond)
	cancel()
	err = <-done
	if err == nil || (!errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled")) {
		t.Fatalf("unexpected loop error: %v", err)
	}

	list, err := workers.List(context.Background())
	if err != nil {
		t.Fatalf("list workers: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("unexpected worker count: %d", len(list))
	}
	if list[0].ID != "worker-loop" {
		t.Fatalf("unexpected worker id: %s", list[0].ID)
	}
	if list[0].LastHeartbeatAt.IsZero() {
		t.Fatalf("expected heartbeat timestamp")
	}
}
