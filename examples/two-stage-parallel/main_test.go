package main

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/scheduler"
	"github.com/ninenhan/go-workflow/worker"
	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestTwoStageDefinitionSharesABCWithEveryConsumer(t *testing.T) {
	workerService, err := worker.NewService(worker.Options{Enabled: true, RegisterBuiltins: true})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	registry := workerService.UnitRegistry()
	if err := registry.RegisterAll(
		workerunit.Registration{
			Name: parallelGroupRef,
			Factory: func() workerunit.ExecutableUnit {
				return &ParallelGroupUnit{registry: registry}
			},
		},
		workerunit.Registration{Name: produceRef, Factory: func() workerunit.ExecutableUnit { return &DemoProduceUnit{} }},
		workerunit.Registration{Name: consumeRef, Factory: func() workerunit.ExecutableUnit { return &DemoConsumeUnit{} }},
	); err != nil {
		t.Fatalf("register units: %v", err)
	}
	service, err := scheduler.NewService(scheduler.Options{
		EnableEmbeddedWorker: true,
		EmbeddedWorker:       workerService,
	})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Shutdown(shutdownContext); err != nil {
			t.Errorf("shutdown scheduler: %v", err)
		}
	})

	run, err := service.RunDefinition(context.Background(), twoStageDefinition(), nil)
	if err != nil {
		t.Fatalf("run definition: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess {
		t.Fatalf("status = %s", run.Status)
	}
	if len(run.NodeRuns) != 2 {
		t.Fatalf("workflow node count = %d, want 2", len(run.NodeRuns))
	}

	abc := resultMap(t, run.Context.NodeResults["stage-abc"])
	if len(abc) != 3 {
		t.Fatalf("stage-abc result = %#v", abc)
	}
	ef := resultMap(t, run.Context.NodeResults["stage-ef"])
	for _, consumerID := range []string{"E", "F"} {
		consumer := resultMap(t, ef[consumerID])
		received := resultMap(t, consumer["received"])
		if !reflect.DeepEqual(received, abc) {
			t.Fatalf("consumer %s received %#v, want %#v", consumerID, received, abc)
		}
	}
}

type concurrencyProbe struct {
	active  atomic.Int32
	peak    atomic.Int32
	entered chan struct{}
	release chan struct{}
}

type concurrencyProbeUnit struct {
	workerunit.Unit
	probe *concurrencyProbe
}

func (u *concurrencyProbeUnit) GetUnitName() string { return "ConcurrencyProbeUnit" }

func (u *concurrencyProbeUnit) GetUnitMeta() *workerunit.Unit { return &u.Unit }

func (u *concurrencyProbeUnit) Execute(
	ctx context.Context,
	_ workerunit.ContextMap,
	self *workerunit.Node,
) (*workerunit.ExecutionResult, error) {
	active := u.probe.active.Add(1)
	defer u.probe.active.Add(-1)
	for {
		peak := u.probe.peak.Load()
		if active <= peak || u.probe.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	u.probe.entered <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-u.probe.release:
		return workerunit.SimpleResult(self.ID), nil
	}
}

func TestParallelGroupUnitHonorsMaxConcurrency(t *testing.T) {
	probe := &concurrencyProbe{
		entered: make(chan struct{}, 3),
		release: make(chan struct{}),
	}
	registry := workerunit.NewRegistry()
	if err := registry.Register("ConcurrencyProbeUnit", func() workerunit.ExecutableUnit {
		return &concurrencyProbeUnit{probe: probe}
	}); err != nil {
		t.Fatalf("register probe: %v", err)
	}
	group := &ParallelGroupUnit{
		registry:       registry,
		MaxConcurrency: 2,
		Units: []UnitCall{
			{ID: "one", Ref: "ConcurrencyProbeUnit"},
			{ID: "two", Ref: "ConcurrencyProbeUnit"},
			{ID: "three", Ref: "ConcurrencyProbeUnit"},
		},
	}

	done := make(chan error, 1)
	go func() {
		_, err := group.Execute(context.Background(), nil, nil)
		done <- err
	}()
	for range 2 {
		select {
		case <-probe.entered:
		case <-time.After(time.Second):
			t.Fatal("two child units did not start concurrently")
		}
	}
	if got := probe.peak.Load(); got != 2 {
		t.Fatalf("peak concurrency = %d, want 2", got)
	}
	if queued := len(probe.entered); queued != 0 {
		t.Fatalf("started %d child units above max_concurrency", queued)
	}
	close(probe.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("execute group: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("parallel group did not finish")
	}
	if got := probe.peak.Load(); got != 2 {
		t.Fatalf("final peak concurrency = %d, want 2", got)
	}
}

func TestParallelGroupUnitRejectsDuplicateIDsBeforeExecution(t *testing.T) {
	registry := workerunit.NewRegistry()
	if err := registry.Register("DemoProduceUnit", func() workerunit.ExecutableUnit {
		return &DemoProduceUnit{}
	}); err != nil {
		t.Fatal(err)
	}
	group := &ParallelGroupUnit{
		registry:       registry,
		MaxConcurrency: 2,
		Units: []UnitCall{
			{ID: "duplicate", Ref: "DemoProduceUnit", Input: "one"},
			{ID: "duplicate", Ref: "DemoProduceUnit", Input: "two"},
		},
	}
	if _, err := group.Execute(context.Background(), nil, nil); err == nil {
		t.Fatal("expected duplicate child unit IDs to fail")
	}
}

func resultMap(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", value)
	}
	return result
}
