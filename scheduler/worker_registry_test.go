package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

func TestMemoryWorkerRegistry_AcquireForTaskWithLabelsAndCapacity(t *testing.T) {
	reg := NewMemoryWorkerRegistry()
	if err := reg.Register(context.Background(), workerproto.WorkerDescriptor{
		ID:                     "worker-a",
		Endpoint:               "http://a",
		Status:                 workerproto.StatusOnline,
		MaxConcurrent:          1,
		SupportedExecutorTypes: []string{string(executor.TypeUnit)},
		SupportedExecutorRefs:  []string{"LogUnit"},
		Labels:                 map[string]string{"region": "us"},
	}); err != nil {
		t.Fatalf("register worker-a: %v", err)
	}
	if err := reg.Register(context.Background(), workerproto.WorkerDescriptor{
		ID:                     "worker-b",
		Endpoint:               "http://b",
		Status:                 workerproto.StatusOnline,
		MaxConcurrent:          2,
		SupportedExecutorTypes: []string{string(executor.TypeUnit)},
		SupportedExecutorRefs:  []string{"LogUnit"},
		Labels:                 map[string]string{"region": "cn"},
	}); err != nil {
		t.Fatalf("register worker-b: %v", err)
	}

	lease, err := reg.AcquireForTask(context.Background(), executor.ExecuteTask{
		RunID:        "run-1",
		NodeID:       "node-1",
		ExecutorType: string(executor.TypeUnit),
		ExecutorRef:  "LogUnit",
		Params: map[string]any{
			"worker_labels": map[string]any{"region": "cn"},
		},
	})
	if err != nil {
		t.Fatalf("acquire worker by labels: %v", err)
	}
	if lease.Worker == nil || lease.Worker.ID != "worker-b" {
		t.Fatalf("unexpected leased worker: %+v", lease.Worker)
	}

	usLease, err := reg.AcquireForTask(context.Background(), executor.ExecuteTask{
		RunID:        "run-1",
		NodeID:       "node-2",
		ExecutorType: string(executor.TypeUnit),
		ExecutorRef:  "LogUnit",
		Params: map[string]any{
			"worker_id": "worker-a",
		},
	})
	if err != nil {
		t.Fatalf("acquire worker-a: %v", err)
	}
	if usLease.Worker == nil || usLease.Worker.ID != "worker-a" {
		t.Fatalf("unexpected worker for worker_id: %+v", usLease.Worker)
	}

	_, err = reg.AcquireForTask(context.Background(), executor.ExecuteTask{
		RunID:        "run-1",
		NodeID:       "node-3",
		ExecutorType: string(executor.TypeUnit),
		ExecutorRef:  "LogUnit",
		Params: map[string]any{
			"worker_id": "worker-a",
		},
	})
	if err == nil {
		t.Fatalf("expected capacity error for worker-a")
	}

	usLease.Release()

	leaseAgain, err := reg.AcquireForTask(context.Background(), executor.ExecuteTask{
		RunID:        "run-1",
		NodeID:       "node-4",
		ExecutorType: string(executor.TypeUnit),
		ExecutorRef:  "LogUnit",
		Params: map[string]any{
			"worker_id": "worker-a",
		},
	})
	if err != nil {
		t.Fatalf("acquire worker-a after release: %v", err)
	}
	if leaseAgain.Worker == nil || leaseAgain.Worker.ID != "worker-a" {
		t.Fatalf("unexpected worker after release: %+v", leaseAgain.Worker)
	}
	leaseAgain.Release()
	lease.Release()
}

func TestMemoryWorkerRegistry_PreferenceTenantAndHeartbeatTTL(t *testing.T) {
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	reg := NewMemoryWorkerRegistry()
	reg.Now = func() time.Time { return now }
	reg.HeartbeatTTL = time.Minute

	if err := reg.Register(context.Background(), workerproto.WorkerDescriptor{
		ID:                     "worker-cn-low",
		Endpoint:               "http://cn-low",
		Tenant:                 "tenant-a",
		Weight:                 1,
		Status:                 workerproto.StatusOnline,
		SupportedExecutorTypes: []string{string(executor.TypeUnit)},
		SupportedExecutorRefs:  []string{"LogUnit"},
		Labels:                 map[string]string{"region": "cn", "tier": "std"},
	}); err != nil {
		t.Fatalf("register worker-cn-low: %v", err)
	}
	if err := reg.Register(context.Background(), workerproto.WorkerDescriptor{
		ID:                     "worker-cn-high",
		Endpoint:               "http://cn-high",
		Tenant:                 "tenant-a",
		Weight:                 5,
		Status:                 workerproto.StatusOnline,
		SupportedExecutorTypes: []string{string(executor.TypeUnit)},
		SupportedExecutorRefs:  []string{"LogUnit"},
		Labels:                 map[string]string{"region": "cn", "tier": "gpu"},
	}); err != nil {
		t.Fatalf("register worker-cn-high: %v", err)
	}
	if err := reg.Register(context.Background(), workerproto.WorkerDescriptor{
		ID:                     "worker-us",
		Endpoint:               "http://us",
		Tenant:                 "tenant-b",
		Weight:                 10,
		Status:                 workerproto.StatusOnline,
		SupportedExecutorTypes: []string{string(executor.TypeUnit)},
		SupportedExecutorRefs:  []string{"LogUnit"},
		Labels:                 map[string]string{"region": "us", "tier": "gpu"},
	}); err != nil {
		t.Fatalf("register worker-us: %v", err)
	}

	selected, err := reg.FindForTask(context.Background(), executor.ExecuteTask{
		RunID:        "run-2",
		NodeID:       "node-1",
		ExecutorType: string(executor.TypeUnit),
		ExecutorRef:  "LogUnit",
		Params: map[string]any{
			"tenant":                  "tenant-a",
			"preferred_worker_labels": map[string]any{"tier": "gpu"},
		},
	})
	if err != nil {
		t.Fatalf("find worker with preference: %v", err)
	}
	if selected == nil || selected.ID != "worker-cn-high" {
		t.Fatalf("unexpected selected worker: %+v", selected)
	}

	now = now.Add(2 * time.Minute)
	list, err := reg.List(context.Background())
	if err != nil {
		t.Fatalf("list workers: %v", err)
	}
	for _, worker := range list {
		if worker.Status != workerproto.StatusOffline {
			t.Fatalf("expected worker %s to be offline after ttl, got %s", worker.ID, worker.Status)
		}
	}
}
