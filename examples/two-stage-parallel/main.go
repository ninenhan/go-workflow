package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	workflow "github.com/ninenhan/go-workflow"
	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/worker"
	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

const (
	parallelGroupRef = "ParallelGroupUnit"
	produceRef       = "DemoProduceUnit"
	consumeRef       = "DemoConsumeUnit"
)

type DemoProduceUnit struct {
	workerunit.Unit
	DelayMS int `json:"delay_ms"`
}

func (u *DemoProduceUnit) GetUnitName() string { return produceRef }

func (u *DemoProduceUnit) GetUnitMeta() *workerunit.Unit { return &u.Unit }

func (u *DemoProduceUnit) Execute(
	ctx context.Context,
	_ workerunit.ContextMap,
	self *workerunit.Node,
) (*workerunit.ExecutionResult, error) {
	if self == nil || self.ID == "" || self.Input == nil {
		return nil, errors.New("DemoProduceUnit: id and input are required")
	}
	if u.DelayMS < 0 {
		return nil, errors.New("DemoProduceUnit: delay_ms must not be negative")
	}
	if u.DelayMS > 0 {
		timer := time.NewTimer(time.Duration(u.DelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return workerunit.SimpleResult(map[string]any{
		"producer": self.ID,
		"value":    self.Input.Data,
	}), nil
}

type DemoConsumeUnit struct {
	workerunit.Unit
	From string `json:"from"`
}

func (u *DemoConsumeUnit) GetUnitName() string { return consumeRef }

func (u *DemoConsumeUnit) GetUnitMeta() *workerunit.Unit { return &u.Unit }

func (u *DemoConsumeUnit) Execute(
	_ context.Context,
	state workerunit.ContextMap,
	self *workerunit.Node,
) (*workerunit.ExecutionResult, error) {
	if self == nil || self.ID == "" {
		return nil, errors.New("DemoConsumeUnit: id is required")
	}
	upstream, ok := state[u.From]
	if !ok || upstream == nil {
		return nil, fmt.Errorf("DemoConsumeUnit %s: upstream result %q is unavailable", self.ID, u.From)
	}
	results, ok := upstream.Data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("DemoConsumeUnit %s: upstream result %q has type %T", self.ID, u.From, upstream.Data)
	}
	for _, required := range []string{"A", "B", "C"} {
		if _, exists := results[required]; !exists {
			return nil, fmt.Errorf("DemoConsumeUnit %s: upstream result is missing %s", self.ID, required)
		}
	}

	// Results are treated as immutable and can therefore be shared safely by
	// every concurrently executing consumer.
	return workerunit.SimpleResult(map[string]any{
		"consumer": self.ID,
		"received": results,
	}), nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (runErr error) {
	ctx := context.Background()
	application, err := workflow.OpenMemoryWithOptions(ctx, workflow.RuntimeOptions{
		Builtins: workflow.BuiltinStandard,
		ConfigureWorker: func(workerService *worker.Service) error {
			unitRegistry := workerService.UnitRegistry()
			return unitRegistry.RegisterAll(
				workerunit.Registration{
					Name: parallelGroupRef,
					Factory: func() workerunit.ExecutableUnit {
						return &ParallelGroupUnit{registry: unitRegistry}
					},
				},
				workerunit.Registration{Name: produceRef, Factory: func() workerunit.ExecutableUnit { return &DemoProduceUnit{} }},
				workerunit.Registration{Name: consumeRef, Factory: func() workerunit.ExecutableUnit { return &DemoConsumeUnit{} }},
			)
		},
	})
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, application.Close())
	}()

	runResult, err := application.Scheduler.RunDefinition(ctx, twoStageDefinition(), nil)
	if err != nil {
		return err
	}
	if runResult.Status != wfruntime.StatusSuccess {
		return fmt.Errorf("workflow failed: %s", runResult.Status)
	}

	encoded, err := json.MarshalIndent(runResult.Context.NodeResults, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("run=%s status=%s workflow_nodes=%d\n%s\n", runResult.ID, runResult.Status, len(runResult.NodeRuns), encoded)
	return nil
}

func twoStageDefinition() *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		ID:         "two-stage-parallel",
		Name:       "[A,B,C] -> [E,F]",
		EntryNodes: []string{"stage-abc"},
		Nodes: []definition.Node{
			{
				ID:       "stage-abc",
				Name:     "并发运行 A、B、C",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: parallelGroupRef},
				Params: map[string]any{
					"max_concurrency": 3,
					"units": []UnitCall{
						{ID: "A", Ref: produceRef, Input: "result-A", Params: map[string]any{"delay_ms": 90}},
						{ID: "B", Ref: produceRef, Input: "result-B", Params: map[string]any{"delay_ms": 60}},
						{ID: "C", Ref: produceRef, Input: "result-C", Params: map[string]any{"delay_ms": 30}},
					},
				},
			},
			{
				ID:       "stage-ef",
				Name:     "并发运行 E、F",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: parallelGroupRef},
				Params: map[string]any{
					"max_concurrency": 2,
					"units": []UnitCall{
						{ID: "E", Ref: consumeRef, Params: map[string]any{"from": "stage-abc"}},
						{ID: "F", Ref: consumeRef, Params: map[string]any{"from": "stage-abc"}},
					},
				},
			},
		},
		Edges: []definition.Edge{{From: "stage-abc", To: "stage-ef"}},
	}
}
