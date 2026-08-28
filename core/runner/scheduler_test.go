package runner

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/planning"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
)

func TestRetryBackoff_UsesExponentialDelayRequestAndMaximum(t *testing.T) {
	policy := planning.RetryPolicy{
		Backoff:    100 * time.Millisecond,
		MaxBackoff: 250 * time.Millisecond,
	}
	tests := []struct {
		attempt   int
		requested time.Duration
		want      time.Duration
	}{
		{attempt: 1, want: 100 * time.Millisecond},
		{attempt: 2, want: 200 * time.Millisecond},
		{attempt: 3, want: 250 * time.Millisecond},
		{attempt: 1, requested: 225 * time.Millisecond, want: 225 * time.Millisecond},
		{attempt: 1, requested: time.Second, want: 250 * time.Millisecond},
	}
	for _, test := range tests {
		if got := retryBackoff(policy, test.attempt, test.requested); got != test.want {
			t.Errorf("attempt %d requested %s: got %s want %s", test.attempt, test.requested, got, test.want)
		}
	}
}

func TestDefaultScheduler_BoundedParallelFiveJoinThree(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var active atomic.Int32
	var maximum atomic.Int32
	var firstPhaseDone atomic.Int32
	var secondPhaseStartedEarly atomic.Bool
	var calls sync.Map
	local.Register("parallel", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		if strings.HasPrefix(req.NodeID, "b") && firstPhaseDone.Load() != 5 {
			secondPhaseStartedEarly.Store(true)
		}
		countValue, _ := calls.LoadOrStore(req.NodeID, new(atomic.Int32))
		count := countValue.(*atomic.Int32).Add(1)
		select {
		case <-ctx.Done():
			return executor.Result{}, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
		if req.NodeID == "a3" && count == 1 {
			return executor.Result{Status: executor.StatusRetryable, Error: "transient", RetryAfter: time.Nanosecond}, nil
		}
		if strings.HasPrefix(req.NodeID, "a") {
			firstPhaseDone.Add(1)
		}
		return executor.Result{Output: req.NodeID}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register executor: %v", err)
	}

	plan := parallelFiveJoinThreePlan()
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run parallel plan: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess || len(run.NodeRuns) != 8 {
		t.Fatalf("unexpected run: status=%s node_runs=%d", run.Status, len(run.NodeRuns))
	}
	if maximum.Load() != 5 {
		t.Fatalf("maximum concurrency = %d, want 5", maximum.Load())
	}
	if secondPhaseStartedEarly.Load() {
		t.Fatal("second phase started before all five first-phase tasks completed")
	}
	if run.NodeRuns["a3"].Attempt != 2 {
		t.Fatalf("a3 attempts = %d, want 2", run.NodeRuns["a3"].Attempt)
	}
}

func TestDefaultScheduler_FailFastCancelsReadyAndDependentTasks(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("fail-fast", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		if req.NodeID == "a1" {
			return executor.Result{Status: executor.StatusFailed, Error: "boom"}, nil
		}
		<-ctx.Done()
		return executor.Result{}, ctx.Err()
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	plan := parallelFiveJoinThreePlan()
	plan.FailFast = true
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("fail-fast run returned infrastructure error: %v", err)
	}
	if run.Status != wfruntime.StatusFailed || run.NodeRuns["a1"].Status != wfruntime.StatusFailed {
		t.Fatalf("failure did not propagate: run=%s a1=%s", run.Status, run.NodeRuns["a1"].Status)
	}
	for _, id := range []string{"b1", "b2", "b3"} {
		if run.NodeRuns[id].Status != wfruntime.StatusCancelled {
			t.Fatalf("dependent %s status = %s", id, run.NodeRuns[id].Status)
		}
	}
}

func TestDefaultScheduler_RecoversInterruptedTaskAttempt(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("recover", func(context.Context, executor.Request) (executor.Result, error) {
		return executor.Result{Output: "recovered"}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	plan := singleRetryTestPlan("recover", 3)
	run := PrepareRun(plan, nil)
	run.StartedAt = time.Now().Add(-time.Minute)
	run.Status = wfruntime.StatusRunning
	run.CurrentNodes = []string{"action"}
	run.NodeRuns["action"].Status = wfruntime.StatusRunning
	run.NodeRuns["action"].Attempt = 1
	store := wfruntime.NewMemoryStore()
	result, err := NewDefaultScheduler(reg, store).Run(context.Background(), plan, run)
	if err != nil {
		t.Fatalf("recover run: %v", err)
	}
	if result.Status != wfruntime.StatusSuccess || result.NodeRuns["action"].Attempt != 2 {
		t.Fatalf("unexpected recovered run: %+v", result.NodeRuns["action"])
	}
	events, err := store.Events(context.Background(), result.ID)
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) == 0 || events[0].Type != wfruntime.EventRunResumed {
		t.Fatalf("first recovery event = %#v", events)
	}
}

func TestDefaultScheduler_ConcurrencyGroupLimitsOnlyItsTasks(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var groupActive atomic.Int32
	var groupMaximum atomic.Int32
	var totalActive atomic.Int32
	var totalMaximum atomic.Int32
	local.Register("grouped", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		total := totalActive.Add(1)
		defer totalActive.Add(-1)
		updateAtomicMaximum(&totalMaximum, total)
		if strings.HasPrefix(req.NodeID, "group-") {
			active := groupActive.Add(1)
			defer groupActive.Add(-1)
			updateAtomicMaximum(&groupMaximum, active)
		}
		select {
		case <-ctx.Done():
			return executor.Result{}, ctx.Err()
		case <-time.After(30 * time.Millisecond):
			return executor.Result{Output: req.NodeID}, nil
		}
	})
	if err := reg.Register(local); err != nil {
		t.Fatal(err)
	}

	plan := independentCapacityPlan("grouped", 6)
	plan.ConcurrencyGroups = map[string]int{"browser": 2}
	for _, id := range []string{"group-1", "group-2", "group-3", "group-4"} {
		node := plan.Nodes[id]
		node.ConcurrencyGroup = "browser"
		plan.Nodes[id] = node
	}
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil || run.Status != wfruntime.StatusSuccess {
		t.Fatalf("run grouped tasks: status=%s err=%v", run.Status, err)
	}
	if groupMaximum.Load() != 2 {
		t.Fatalf("group maximum = %d, want 2", groupMaximum.Load())
	}
	if totalMaximum.Load() != 4 {
		t.Fatalf("total maximum = %d, want 4 (two grouped plus two ungrouped)", totalMaximum.Load())
	}
}

func TestDefaultScheduler_ResourcePoolIsSharedAcrossRuns(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var active atomic.Int32
	var maximum atomic.Int32
	local.Register("pooled", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		current := active.Add(1)
		defer active.Add(-1)
		updateAtomicMaximum(&maximum, current)
		select {
		case <-ctx.Done():
			return executor.Result{}, ctx.Err()
		case <-time.After(40 * time.Millisecond):
			return executor.Result{Output: req.NodeID}, nil
		}
	})
	if err := reg.Register(local); err != nil {
		t.Fatal(err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := independentCapacityPlan("pooled", 1)
	plan.ResourcePools = map[string]int{"chromium": 1}
	node := plan.Nodes[plan.TopologicalOrder[0]]
	node.ResourcePool = "chromium"
	plan.Nodes[node.ID] = node

	results := make(chan error, 2)
	for range 2 {
		go func() {
			run, err := scheduler.Run(context.Background(), plan, nil)
			if err == nil && run.Status != wfruntime.StatusSuccess {
				err = fmt.Errorf("run status %s", run.Status)
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("run pooled task: %v", err)
		}
	}
	if maximum.Load() != 1 {
		t.Fatalf("shared resource maximum = %d, want 1", maximum.Load())
	}
}

func TestDefaultScheduler_ResourcePoolReleasesBeforeRetry(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var calls atomic.Int32
	var active atomic.Int32
	var maximum atomic.Int32
	local.Register("pooled-retry", func(context.Context, executor.Request) (executor.Result, error) {
		current := active.Add(1)
		defer active.Add(-1)
		updateAtomicMaximum(&maximum, current)
		if calls.Add(1) == 1 {
			return executor.Result{Status: executor.StatusRetryable, Error: "retry", RetryAfter: time.Nanosecond}, nil
		}
		return executor.Result{Output: "ok"}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatal(err)
	}
	plan := independentCapacityPlan("pooled-retry", 2)
	plan.ResourcePools = map[string]int{"chromium": 1}
	for id, node := range plan.Nodes {
		node.ResourcePool = "chromium"
		node.Retry = planning.RetryPolicy{MaxAttempts: 2, Backoff: time.Nanosecond, MaxBackoff: time.Microsecond}
		plan.Nodes[id] = node
	}
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil || run.Status != wfruntime.StatusSuccess {
		t.Fatalf("run pooled retry: status=%s err=%v", run.Status, err)
	}
	if calls.Load() != 3 || maximum.Load() != 1 {
		t.Fatalf("calls=%d maximum=%d, want calls=3 maximum=1", calls.Load(), maximum.Load())
	}
}

func independentCapacityPlan(ref string, count int) *planning.ExecutionPlan {
	nodes := make(map[string]planning.PlanNode, count)
	dependencies := make(map[string][]string, count)
	order := make([]string, 0, count)
	for index := 1; index <= count; index++ {
		prefix := "group-"
		if index > 4 {
			prefix = "free-"
		}
		id := fmt.Sprintf("%s%d", prefix, index)
		order = append(order, id)
		nodes[id] = planning.PlanNode{
			ID: id, ExecutorType: string(executor.TypeLocalGo), ExecutorRef: ref,
			Params: map[string]any{"fn": ref}, Retry: planning.RetryPolicy{MaxAttempts: 1},
		}
		dependencies[id] = []string{}
	}
	return &planning.ExecutionPlan{
		PlanID: "plan-" + ref, WorkflowID: "wf-" + ref, WorkflowVersionID: "v1",
		MaxConcurrency: count, TopologicalOrder: order, Dependencies: dependencies, Nodes: nodes,
	}
}

func updateAtomicMaximum(maximum *atomic.Int32, current int32) {
	for {
		observed := maximum.Load()
		if current <= observed || maximum.CompareAndSwap(observed, current) {
			return
		}
	}
}

func parallelFiveJoinThreePlan() *planning.ExecutionPlan {
	nodes := make(map[string]planning.PlanNode, 8)
	dependencies := make(map[string][]string, 8)
	order := []string{"a1", "a2", "a3", "a4", "a5", "b1", "b2", "b3"}
	for _, id := range order {
		nodes[id] = planning.PlanNode{
			ID:           id,
			ExecutorType: string(executor.TypeLocalGo),
			ExecutorRef:  "parallel",
			Params:       map[string]any{"fn": "parallel"},
			Retry:        planning.RetryPolicy{MaxAttempts: 2, Backoff: time.Nanosecond, MaxBackoff: time.Microsecond},
		}
		if strings.HasPrefix(id, "b") {
			dependencies[id] = []string{"a1", "a2", "a3", "a4", "a5"}
		} else {
			dependencies[id] = []string{}
		}
	}
	return &planning.ExecutionPlan{
		PlanID:            "plan-five-join-three",
		WorkflowID:        "wf-five-join-three",
		WorkflowVersionID: "v1",
		MaxConcurrency:    5,
		TopologicalOrder:  order,
		Dependencies:      dependencies,
		Nodes:             nodes,
	}
}

func TestDefaultScheduler_RetriesOnlyRetryableExecutorResults(t *testing.T) {
	t.Run("retryable result succeeds on the next attempt", func(t *testing.T) {
		var calls atomic.Int32
		reg := executor.NewRegistry()
		local := executor.NewLocalExecutor()
		local.Register("transient", func(context.Context, executor.Request) (executor.Result, error) {
			if calls.Add(1) == 1 {
				return executor.Result{
					Status:     executor.StatusRetryable,
					Error:      "temporary failure",
					RetryAfter: time.Nanosecond,
				}, nil
			}
			return executor.Result{Output: "recovered"}, nil
		})
		if err := reg.Register(local); err != nil {
			t.Fatalf("register local executor: %v", err)
		}
		plan := singleRetryTestPlan("transient", 3)
		run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
		if err != nil {
			t.Fatalf("run retryable plan: %v", err)
		}
		nodeRun := run.NodeRuns["action"]
		if run.Status != wfruntime.StatusSuccess || calls.Load() != 2 || nodeRun.Attempt != 2 || nodeRun.Error != "" {
			t.Fatalf("unexpected recovered run: status=%s calls=%d node=%#v", run.Status, calls.Load(), nodeRun)
		}
	})

	t.Run("permanent result does not consume retry budget", func(t *testing.T) {
		var calls atomic.Int32
		reg := executor.NewRegistry()
		local := executor.NewLocalExecutor()
		local.Register("permanent", func(context.Context, executor.Request) (executor.Result, error) {
			calls.Add(1)
			return executor.Result{Status: executor.StatusFailed, Error: "invalid request"}, nil
		})
		if err := reg.Register(local); err != nil {
			t.Fatalf("register local executor: %v", err)
		}
		plan := singleRetryTestPlan("permanent", 3)
		run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
		if err != nil {
			t.Fatalf("run permanent plan: %v", err)
		}
		nodeRun := run.NodeRuns["action"]
		if run.Status != wfruntime.StatusFailed || calls.Load() != 1 || nodeRun.Attempt != 1 || nodeRun.Error != "invalid request" {
			t.Fatalf("unexpected permanent run: status=%s calls=%d node=%#v", run.Status, calls.Load(), nodeRun)
		}
	})
}

func singleRetryTestPlan(ref string, maxAttempts int) *planning.ExecutionPlan {
	return &planning.ExecutionPlan{
		PlanID:            "plan-retry-" + ref,
		WorkflowID:        "workflow-retry",
		WorkflowVersionID: "v1",
		EntryNodes:        []string{"action"},
		ExitNodes:         []string{"action"},
		TopologicalOrder:  []string{"action"},
		Dependencies:      map[string][]string{"action": {}},
		Nodes: map[string]planning.PlanNode{
			"action": {
				ID:           "action",
				Name:         "Action",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  ref,
				Params:       map[string]any{"fn": ref},
				Retry: planning.RetryPolicy{
					MaxAttempts: maxAttempts,
					Backoff:     time.Nanosecond,
					MaxBackoff:  time.Microsecond,
				},
			},
		},
	}
}

func TestDefaultScheduler_Run(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("echo", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-1",
		WorkflowID:        "wf-1",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"a", "b"},
		Dependencies: map[string][]string{
			"a": []string{},
			"b": {"a"},
		},
		Nodes: map[string]planning.PlanNode{
			"a": {
				ID:           "a",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "echo",
				Input:        "hello",
				Params:       map[string]any{"fn": "echo"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
			"b": {
				ID:           "b",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "echo",
				Input:        "world",
				Params:       map[string]any{"fn": "echo"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
		},
	}

	run, err := scheduler.Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected run status: %s", run.Status)
	}
	if len(run.CurrentNodes) != 0 {
		t.Fatalf("completed run still has current nodes: %#v", run.CurrentNodes)
	}
	if run.Context.NodeResults["b"] != "world" {
		t.Fatalf("unexpected node result: %#v", run.Context.NodeResults)
	}
	if run.NodeRuns["a"] == nil || run.NodeRuns["a"].Input != "hello" {
		t.Fatalf("unexpected node input capture for a: %#v", run.NodeRuns["a"])
	}
	if run.NodeRuns["b"] == nil || run.NodeRuns["b"].Input != "world" {
		t.Fatalf("unexpected node input capture for b: %#v", run.NodeRuns["b"])
	}
}

func TestDefaultScheduler_Run_NodeLoop(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var count atomic.Int32
	local.Register("loop", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		current := count.Add(1)
		return executor.Result{
			Output: map[string]any{
				"count": current,
			},
		}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-loop",
		WorkflowID:        "wf-loop",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"loop"},
		Dependencies: map[string][]string{
			"loop": {},
		},
		Nodes: map[string]planning.PlanNode{
			"loop": {
				ID:           "loop",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "loop",
				Params:       map[string]any{"fn": "loop"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
				Loop: &planning.LoopPolicy{
					MaxIterations: 5,
					Condition:     "Iteration < 3",
				},
			},
		},
	}

	run, err := scheduler.Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected run status: %s", run.Status)
	}
	if got := count.Load(); got != 3 {
		t.Fatalf("unexpected loop execution count: %d", got)
	}
	node := run.NodeRuns["loop"]
	if node == nil || node.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected node run: %+v", node)
	}
	if node.Metadata["loop_iteration"] != 3 {
		t.Fatalf("unexpected loop iteration metadata: %#v", node.Metadata["loop_iteration"])
	}
}

func TestDefaultScheduler_Run_DynamicNodeLoopCount(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var count atomic.Int32
	local.Register("loop", func(_ context.Context, _ executor.Request) (executor.Result, error) {
		return executor.Result{Output: count.Add(1)}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	plan := &planning.ExecutionPlan{
		PlanID: "plan-dynamic-node-loop", WorkflowID: "wf-dynamic-node-loop", WorkflowVersionID: "v1",
		EntryNodes: []string{"loop"}, ExitNodes: []string{"loop"}, TopologicalOrder: []string{"loop"},
		Adjacency: map[string][]string{"loop": {}}, Dependencies: map[string][]string{"loop": {}},
		Nodes: map[string]planning.PlanNode{
			"loop": {
				ID: "loop", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "loop",
				Params: map[string]any{"fn": "loop"}, Retry: planning.RetryPolicy{MaxAttempts: 1},
				Loop: &planning.LoopPolicy{
					Mode:          "count",
					MaxIterations: 1000,
					CountBinding:  &planning.InputBinding{Source: "var", From: "repeat_count", Required: true},
				},
			},
		},
	}
	runInput := wfruntime.NewWorkflowRun("run-dynamic-node-loop", plan.WorkflowID, plan.WorkflowVersionID, plan.PlanID)
	runInput.Context.Variables["repeat_count"] = "3"
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, runInput)
	if err != nil {
		t.Fatalf("run dynamic node loop: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess || count.Load() != 3 {
		t.Fatalf("unexpected dynamic node loop result: status=%s calls=%d", run.Status, count.Load())
	}
	if got := run.NodeRuns["loop"].Metadata[resolvedLoopCountMetadataKey]; got != 3 {
		t.Fatalf("resolved node loop count was not persisted: %#v", got)
	}
}

func TestDefaultScheduler_Run_RejectsInvalidDynamicNodeLoopCount(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "fraction", value: 1.5, want: "whole number"},
		{name: "zero", value: 0, want: "between 1 and 1000"},
		{name: "above maximum", value: 1001, want: "between 1 and 1000"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reg := executor.NewRegistry()
			local := executor.NewLocalExecutor()
			var calls atomic.Int32
			local.Register("loop", func(_ context.Context, _ executor.Request) (executor.Result, error) {
				calls.Add(1)
				return executor.Result{Output: nil}, nil
			})
			if err := reg.Register(local); err != nil {
				t.Fatalf("register local executor: %v", err)
			}
			plan := &planning.ExecutionPlan{
				PlanID: "plan-invalid-dynamic-loop", WorkflowID: "wf-invalid-dynamic-loop", WorkflowVersionID: "v1",
				EntryNodes: []string{"loop"}, ExitNodes: []string{"loop"}, TopologicalOrder: []string{"loop"},
				Adjacency: map[string][]string{"loop": {}}, Dependencies: map[string][]string{"loop": {}},
				Nodes: map[string]planning.PlanNode{
					"loop": {
						ID: "loop", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "loop",
						Params: map[string]any{"fn": "loop"}, Retry: planning.RetryPolicy{MaxAttempts: 1},
						Loop: &planning.LoopPolicy{
							Mode:          "count",
							MaxIterations: 1000,
							CountBinding:  &planning.InputBinding{Source: "var", From: "repeat_count", Required: true},
						},
					},
				},
			}
			runInput := wfruntime.NewWorkflowRun("run-invalid-dynamic-loop", plan.WorkflowID, plan.WorkflowVersionID, plan.PlanID)
			runInput.Context.Variables["repeat_count"] = test.value
			run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, runInput)
			if err != nil {
				t.Fatalf("run invalid dynamic loop: %v", err)
			}
			if run.Status != wfruntime.StatusFailed || calls.Load() != 0 {
				t.Fatalf("invalid count executed: status=%s calls=%d", run.Status, calls.Load())
			}
			if got := run.NodeRuns["loop"].Error; !strings.Contains(got, test.want) {
				t.Fatalf("unexpected invalid count error: %q", got)
			}
		})
	}
}

func TestDefaultScheduler_Run_EachItemLoop(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var received []string
	local.Register("uppercase", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		item, ok := req.Input.(string)
		if !ok {
			t.Fatalf("each-item input must be one string, got %T", req.Input)
		}
		received = append(received, item)
		return executor.Result{Output: strings.ToUpper(item)}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-each",
		WorkflowID:        "wf-each",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"each"},
		Dependencies:      map[string][]string{"each": {}},
		Nodes: map[string]planning.PlanNode{
			"each": {
				ID:           "each",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "uppercase",
				Input:        []any{"alpha", "beta", "gamma"},
				Params:       map[string]any{"fn": "uppercase"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
				Loop:         &planning.LoopPolicy{Mode: "each", MaxIterations: 1000},
			},
		},
	}

	run, err := scheduler.Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run each-item loop: %v", err)
	}
	if !reflect.DeepEqual(received, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("unexpected each-item inputs: %#v", received)
	}
	want := []any{"ALPHA", "BETA", "GAMMA"}
	if !reflect.DeepEqual(run.Context.NodeResults["each"], want) {
		t.Fatalf("unexpected aggregate output: %#v", run.Context.NodeResults["each"])
	}
	node := run.NodeRuns["each"]
	if !reflect.DeepEqual(node.Result, want) || node.Metadata["loop_iteration"] != 3 {
		t.Fatalf("unexpected each-item node run: %#v", node)
	}
	if _, exists := node.Metadata[eachLoopOutputsMetadataKey]; exists {
		t.Fatalf("private each-item outputs leaked after completion: %#v", node.Metadata)
	}
}

func TestDefaultScheduler_Run_EmptyEachItemLoop(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var calls atomic.Int32
	local.Register("never", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		calls.Add(1)
		return executor.Result{Output: req.Input}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}
	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-empty-each",
		WorkflowID:        "wf-empty-each",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"each"},
		Dependencies:      map[string][]string{"each": {}},
		Nodes: map[string]planning.PlanNode{
			"each": {
				ID:           "each",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "never",
				Input:        []any{},
				Params:       map[string]any{"fn": "never"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
				Loop:         &planning.LoopPolicy{Mode: "each", MaxIterations: 1000},
			},
		},
	}
	run, err := scheduler.Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run empty each-item loop: %v", err)
	}
	if calls.Load() != 0 || run.Status != wfruntime.StatusSuccess {
		t.Fatalf("empty each-item loop executed action: calls=%d status=%s", calls.Load(), run.Status)
	}
	if result, ok := run.Context.NodeResults["each"].([]any); !ok || len(result) != 0 {
		t.Fatalf("unexpected empty each-item output: %#v", run.Context.NodeResults["each"])
	}
}

func TestPrepareEachLoopInputRejectsInvalidInput(t *testing.T) {
	node := planning.PlanNode{ID: "each", Loop: &planning.LoopPolicy{Mode: "each", MaxIterations: 2}}
	typedItems, empty, err := prepareEachLoopInput(node, &wfruntime.NodeRun{}, []int{7, 9})
	if err != nil || empty || typedItems != 7 {
		t.Fatalf("expected typed slice item, got item=%#v empty=%t err=%v", typedItems, empty, err)
	}
	if _, _, err := prepareEachLoopInput(node, &wfruntime.NodeRun{}, "not-a-list"); err == nil || !strings.Contains(err.Error(), "input must be a list") {
		t.Fatalf("expected non-list input error, got %v", err)
	}
	if _, _, err := prepareEachLoopInput(node, &wfruntime.NodeRun{}, []any{1, 2, 3}); err == nil || !strings.Contains(err.Error(), "received 3 items; maximum is 2") {
		t.Fatalf("expected each-item limit error, got %v", err)
	}
}

func TestDefaultScheduler_Run_EachItemLoopGroup(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var received []string
	local.Register("echo", func(_ context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	local.Register("decorate", func(_ context.Context, req executor.Request) (executor.Result, error) {
		value, ok := req.Input.(string)
		if !ok {
			t.Fatalf("group body must receive one string, got %T", req.Input)
		}
		received = append(received, value)
		return executor.Result{Output: value + "!"}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	binding := func(from string) *planning.InputSpec {
		return &planning.InputSpec{Mode: "replace", Bindings: []planning.InputBinding{{Source: "node", From: from, Required: true}}}
	}
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-each-group",
		WorkflowID:        "wf-each-group",
		WorkflowVersionID: "v1",
		EntryNodes:        []string{"start"},
		ExitNodes:         []string{"after"},
		TopologicalOrder:  []string{"start", "body", "end", "after"},
		Adjacency: map[string][]string{
			"start": {"body"}, "body": {"end"}, "end": {"after"}, "after": {},
		},
		Dependencies: map[string][]string{
			"start": {}, "body": {"start"}, "end": {"body"}, "after": {"end"},
		},
		Nodes: map[string]planning.PlanNode{
			"start": {ID: "start", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", Input: []any{"a", "b", "c"}, Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"body":  {ID: "body", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "decorate", InputSpec: binding("start"), Params: map[string]any{"fn": "decorate"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"end":   {ID: "end", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", InputSpec: binding("body"), Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"after": {ID: "after", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", InputSpec: binding("end"), Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
		},
		LoopGroups: map[string]planning.LoopGroup{
			"group": {ID: "group", Start: "start", End: "end", Mode: "each", MaxIterations: 1000, Scope: []string{"start", "body", "end"}},
		},
	}
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run each-item loop group: %v", err)
	}
	if !reflect.DeepEqual(received, []string{"a", "b", "c"}) {
		t.Fatalf("unexpected grouped item inputs: %#v", received)
	}
	want := []any{"a!", "b!", "c!"}
	if !reflect.DeepEqual(run.Context.NodeResults["end"], want) || !reflect.DeepEqual(run.Context.NodeResults["after"], want) {
		t.Fatalf("unexpected grouped outputs: end=%#v after=%#v", run.Context.NodeResults["end"], run.Context.NodeResults["after"])
	}
	if run.NodeRuns["end"].Metadata["loop_iteration"] != 3 {
		t.Fatalf("unexpected grouped iteration metadata: %#v", run.NodeRuns["end"].Metadata)
	}
}

func TestDefaultScheduler_Run_CountLoopGroup(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var bodyCalls atomic.Int32
	local.Register("echo", func(_ context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	local.Register("count", func(_ context.Context, _ executor.Request) (executor.Result, error) {
		return executor.Result{Output: bodyCalls.Add(1)}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	plan := &planning.ExecutionPlan{
		PlanID: "plan-count-group", WorkflowID: "wf-count-group", WorkflowVersionID: "v1",
		EntryNodes: []string{"start"}, ExitNodes: []string{"end"}, TopologicalOrder: []string{"start", "body", "end"},
		Adjacency:    map[string][]string{"start": {"body"}, "body": {"end"}, "end": {}},
		Dependencies: map[string][]string{"start": {}, "body": {"start"}, "end": {"body"}},
		Nodes: map[string]planning.PlanNode{
			"start": {ID: "start", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", Input: "same", Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"body":  {ID: "body", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "count", Params: map[string]any{"fn": "count"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"end":   {ID: "end", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", InputSpec: &planning.InputSpec{Mode: "replace", Bindings: []planning.InputBinding{{Source: "node", From: "body", Required: true}}}, Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
		},
		LoopGroups: map[string]planning.LoopGroup{"group": {ID: "group", Start: "start", End: "end", Mode: "count", MaxIterations: 3, Scope: []string{"start", "body", "end"}}},
	}
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run count loop group: %v", err)
	}
	if bodyCalls.Load() != 3 || !reflect.DeepEqual(run.Context.NodeResults["end"], []any{int32(1), int32(2), int32(3)}) {
		t.Fatalf("unexpected count loop group result: calls=%d output=%#v", bodyCalls.Load(), run.Context.NodeResults["end"])
	}
}

func TestDefaultScheduler_Run_DynamicLoopGroupCount(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var calls atomic.Int32
	local.Register("count", func(_ context.Context, _ executor.Request) (executor.Result, error) {
		return executor.Result{Output: calls.Add(1)}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	plan := &planning.ExecutionPlan{
		PlanID: "plan-dynamic-count-group", WorkflowID: "wf-dynamic-count-group", WorkflowVersionID: "v1",
		EntryNodes: []string{"start"}, ExitNodes: []string{"end"}, TopologicalOrder: []string{"start", "end"},
		Adjacency:    map[string][]string{"start": {"end"}, "end": {}},
		Dependencies: map[string][]string{"start": {}, "end": {"start"}},
		Nodes: map[string]planning.PlanNode{
			"start": {ID: "start", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "count", Params: map[string]any{"fn": "count"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"end":   {ID: "end", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "count", Params: map[string]any{"fn": "count"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
		},
		LoopGroups: map[string]planning.LoopGroup{
			"group": {
				ID: "group", Start: "start", End: "end", Mode: "count", MaxIterations: 1000,
				CountBinding: &planning.InputBinding{Source: "var", From: "repeat_count", Required: true},
				Scope:        []string{"start", "end"},
			},
		},
	}
	runInput := wfruntime.NewWorkflowRun("run-dynamic-count-group", plan.WorkflowID, plan.WorkflowVersionID, plan.PlanID)
	runInput.Context.Variables["repeat_count"] = float64(4)
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, runInput)
	if err != nil {
		t.Fatalf("run dynamic count loop group: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess || calls.Load() != 8 {
		t.Fatalf("unexpected dynamic loop group result: status=%s calls=%d", run.Status, calls.Load())
	}
	if got := run.NodeRuns["start"].Metadata[resolvedLoopCountMetadataKey]; got != 4 {
		t.Fatalf("resolved loop group count was not persisted: %#v", got)
	}
}

func TestDefaultScheduler_Run_EmptyEachItemLoopGroup(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var groupCalls atomic.Int32
	local.Register("group", func(_ context.Context, req executor.Request) (executor.Result, error) {
		groupCalls.Add(1)
		return executor.Result{Output: req.Input}, nil
	})
	local.Register("after", func(_ context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	plan := &planning.ExecutionPlan{
		PlanID: "plan-empty-group", WorkflowID: "wf-empty-group", WorkflowVersionID: "v1",
		EntryNodes: []string{"start"}, ExitNodes: []string{"after"}, TopologicalOrder: []string{"start", "end", "after"},
		Adjacency:    map[string][]string{"start": {"end"}, "end": {"after"}, "after": {}},
		Dependencies: map[string][]string{"start": {}, "end": {"start"}, "after": {"end"}},
		Nodes: map[string]planning.PlanNode{
			"start": {ID: "start", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "group", Input: []any{}, Params: map[string]any{"fn": "group"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"end":   {ID: "end", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "group", Params: map[string]any{"fn": "group"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"after": {ID: "after", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "after", InputSpec: &planning.InputSpec{Mode: "replace", Bindings: []planning.InputBinding{{Source: "node", From: "end", Required: true}}}, Params: map[string]any{"fn": "after"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
		},
		LoopGroups: map[string]planning.LoopGroup{"group": {ID: "group", Start: "start", End: "end", Mode: "each", MaxIterations: 1000, Scope: []string{"start", "end"}}},
	}
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run empty loop group: %v", err)
	}
	if groupCalls.Load() != 0 {
		t.Fatalf("empty loop group invoked an executor %d times", groupCalls.Load())
	}
	if output, ok := run.Context.NodeResults["after"].([]any); !ok || len(output) != 0 {
		t.Fatalf("unexpected empty loop group output: %#v", run.Context.NodeResults["after"])
	}
	for _, key := range []string{planning.LoopGroupItemVariable("group"), planning.LoopGroupIndexVariable("group")} {
		if _, exists := run.Context.Variables[key]; exists {
			t.Fatalf("empty loop group leaked scoped variable %s", key)
		}
	}
}

func TestDefaultScheduler_Run_CountLoopGroupExposesScopedIndex(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("capture-index", func(_ context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Params["repeat_index"]}, nil
	})
	local.Register("echo", func(_ context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	plan := &planning.ExecutionPlan{
		PlanID: "plan-count-variables", WorkflowID: "wf-count-variables", WorkflowVersionID: "v1",
		EntryNodes: []string{"start"}, ExitNodes: []string{"end"}, TopologicalOrder: []string{"start", "end"},
		Adjacency:    map[string][]string{"start": {"end"}, "end": {}},
		Dependencies: map[string][]string{"start": {}, "end": {"start"}},
		Nodes: map[string]planning.PlanNode{
			"start": {
				ID: "start", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "capture-index",
				Params: map[string]any{"fn": "capture-index"},
				ParamBindings: map[string]planning.InputBinding{
					"repeat_index": {Source: "var", From: planning.LoopGroupIndexVariable("group"), Required: true},
				},
				Retry: planning.RetryPolicy{MaxAttempts: 1},
			},
			"end": {
				ID: "end", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo",
				InputSpec: &planning.InputSpec{Mode: "replace", Bindings: []planning.InputBinding{{Source: "node", From: "start", Required: true}}},
				Params:    map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1},
			},
		},
		LoopGroups: map[string]planning.LoopGroup{
			"group": {ID: "group", Start: "start", End: "end", Mode: "count", MaxIterations: 3, Scope: []string{"start", "end"}},
		},
	}
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run count loop group variables: %v", err)
	}
	if want := []any{1, 2, 3}; !reflect.DeepEqual(run.Context.NodeResults["end"], want) {
		t.Fatalf("unexpected repeat indices: got=%#v want=%#v", run.Context.NodeResults["end"], want)
	}
	for _, key := range []string{planning.LoopGroupItemVariable("group"), planning.LoopGroupIndexVariable("group")} {
		if _, exists := run.Context.Variables[key]; exists {
			t.Fatalf("completed count loop group leaked scoped variable %s", key)
		}
	}
}

func TestDefaultScheduler_Run_EachLoopGroupExposesScopedItemAndIndex(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("echo", func(_ context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	plan := &planning.ExecutionPlan{
		PlanID: "plan-each-variables", WorkflowID: "wf-each-variables", WorkflowVersionID: "v1",
		EntryNodes: []string{"start"}, ExitNodes: []string{"end"}, TopologicalOrder: []string{"start", "end"},
		Adjacency:    map[string][]string{"start": {"end"}, "end": {}},
		Dependencies: map[string][]string{"start": {}, "end": {"start"}},
		Nodes: map[string]planning.PlanNode{
			"start": {ID: "start", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", Input: []any{"alpha", "beta"}, Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"end": {
				ID: "end", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo",
				InputSpec: &planning.InputSpec{Mode: "object", Bindings: []planning.InputBinding{
					{Source: "var", From: planning.LoopGroupItemVariable("group"), As: "item", Required: true},
					{Source: "var", From: planning.LoopGroupIndexVariable("group"), As: "index", Required: true},
				}},
				Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1},
			},
		},
		LoopGroups: map[string]planning.LoopGroup{
			"group": {ID: "group", Start: "start", End: "end", Mode: "each", MaxIterations: 1000, Scope: []string{"start", "end"}},
		},
	}
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run each loop group variables: %v", err)
	}
	want := []any{
		map[string]any{"item": "alpha", "index": 1},
		map[string]any{"item": "beta", "index": 2},
	}
	if !reflect.DeepEqual(run.Context.NodeResults["end"], want) {
		t.Fatalf("unexpected repeat item/index values: got=%#v want=%#v", run.Context.NodeResults["end"], want)
	}
	for _, key := range []string{planning.LoopGroupItemVariable("group"), planning.LoopGroupIndexVariable("group")} {
		if _, exists := run.Context.Variables[key]; exists {
			t.Fatalf("completed each loop group leaked scoped variable %s", key)
		}
	}
}

func TestDefaultScheduler_Run_NestedCountLoopGroups(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var bodyCalls atomic.Int32
	local.Register("echo", func(_ context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	local.Register("count", func(_ context.Context, _ executor.Request) (executor.Result, error) {
		return executor.Result{Output: bodyCalls.Add(1)}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	binding := func(from string) *planning.InputSpec {
		return &planning.InputSpec{Mode: "replace", Bindings: []planning.InputBinding{{Source: "node", From: from, Required: true}}}
	}
	plan := &planning.ExecutionPlan{
		PlanID: "plan-nested-count", WorkflowID: "wf-nested-count", WorkflowVersionID: "v1",
		EntryNodes:       []string{"outer-start"},
		ExitNodes:        []string{"outer-end"},
		TopologicalOrder: []string{"outer-start", "inner-start", "body", "inner-end", "outer-end"},
		Adjacency: map[string][]string{
			"outer-start": {"inner-start"},
			"inner-start": {"body"},
			"body":        {"inner-end"},
			"inner-end":   {"outer-end"},
			"outer-end":   {},
		},
		Dependencies: map[string][]string{
			"outer-start": {},
			"inner-start": {"outer-start"},
			"body":        {"inner-start"},
			"inner-end":   {"body"},
			"outer-end":   {"inner-end"},
		},
		Nodes: map[string]planning.PlanNode{
			"outer-start": {ID: "outer-start", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", Input: "seed", Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"inner-start": {ID: "inner-start", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", InputSpec: binding("outer-start"), Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"body":        {ID: "body", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "count", Params: map[string]any{"fn": "count"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"inner-end":   {ID: "inner-end", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", InputSpec: binding("body"), Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"outer-end":   {ID: "outer-end", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", InputSpec: binding("inner-end"), Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
		},
		LoopGroups: map[string]planning.LoopGroup{
			"outer": {ID: "outer", Start: "outer-start", End: "outer-end", Mode: "count", MaxIterations: 2, Scope: []string{"outer-start", "inner-start", "body", "inner-end", "outer-end"}},
			"inner": {ID: "inner", Start: "inner-start", End: "inner-end", Mode: "count", MaxIterations: 3, Scope: []string{"inner-start", "body", "inner-end"}, Parent: "outer", Depth: 1},
		},
	}
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run nested count loop groups: %v", err)
	}
	want := []any{
		[]any{int32(1), int32(2), int32(3)},
		[]any{int32(4), int32(5), int32(6)},
	}
	if bodyCalls.Load() != 6 || !reflect.DeepEqual(run.Context.NodeResults["outer-end"], want) {
		t.Fatalf("unexpected nested count result: calls=%d output=%#v", bodyCalls.Load(), run.Context.NodeResults["outer-end"])
	}
}

func TestDefaultScheduler_Run_NestedEachItemLoopGroupsIncludingEmptyInnerList(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var bodyCalls atomic.Int32
	local.Register("echo", func(_ context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	local.Register("uppercase", func(_ context.Context, req executor.Request) (executor.Result, error) {
		call := bodyCalls.Add(1)
		value, ok := req.Input.(string)
		if !ok {
			t.Fatalf("nested each body must receive one string, got %T", req.Input)
		}
		wantOuterItems := map[int32][]any{1: {"a", "b"}, 2: {"a", "b"}, 3: {"c"}}
		wantOuterIndices := map[int32]int{1: 1, 2: 1, 3: 3}
		wantInnerIndices := map[int32]int{1: 1, 2: 2, 3: 1}
		if got := req.Context[planning.LoopGroupItemVariable("outer")]; !reflect.DeepEqual(got, wantOuterItems[call]) {
			t.Fatalf("unexpected outer repeat item at call %d: %#v", call, got)
		}
		if got := req.Context[planning.LoopGroupIndexVariable("outer")]; got != wantOuterIndices[call] {
			t.Fatalf("unexpected outer repeat index at call %d: %#v", call, got)
		}
		if got := req.Context[planning.LoopGroupItemVariable("inner")]; got != value {
			t.Fatalf("unexpected inner repeat item at call %d: %#v", call, got)
		}
		if got := req.Context[planning.LoopGroupIndexVariable("inner")]; got != wantInnerIndices[call] {
			t.Fatalf("unexpected inner repeat index at call %d: %#v", call, got)
		}
		return executor.Result{Output: strings.ToUpper(value)}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	binding := func(from string) *planning.InputSpec {
		return &planning.InputSpec{Mode: "replace", Bindings: []planning.InputBinding{{Source: "node", From: from, Required: true}}}
	}
	plan := &planning.ExecutionPlan{
		PlanID: "plan-nested-each", WorkflowID: "wf-nested-each", WorkflowVersionID: "v1",
		EntryNodes:       []string{"outer-start"},
		ExitNodes:        []string{"outer-end"},
		TopologicalOrder: []string{"outer-start", "inner-start", "body", "inner-end", "outer-end"},
		Adjacency: map[string][]string{
			"outer-start": {"inner-start"},
			"inner-start": {"body"},
			"body":        {"inner-end"},
			"inner-end":   {"outer-end"},
			"outer-end":   {},
		},
		Dependencies: map[string][]string{
			"outer-start": {},
			"inner-start": {"outer-start"},
			"body":        {"inner-start"},
			"inner-end":   {"body"},
			"outer-end":   {"inner-end"},
		},
		Nodes: map[string]planning.PlanNode{
			"outer-start": {ID: "outer-start", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", Input: []any{[]any{"a", "b"}, []any{}, []any{"c"}}, Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"inner-start": {ID: "inner-start", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", InputSpec: binding("outer-start"), Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"body":        {ID: "body", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "uppercase", InputSpec: binding("inner-start"), Params: map[string]any{"fn": "uppercase"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"inner-end":   {ID: "inner-end", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", InputSpec: binding("body"), Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
			"outer-end":   {ID: "outer-end", ExecutorType: string(executor.TypeLocalGo), ExecutorRef: "echo", InputSpec: binding("inner-end"), Params: map[string]any{"fn": "echo"}, Retry: planning.RetryPolicy{MaxAttempts: 1}},
		},
		LoopGroups: map[string]planning.LoopGroup{
			"outer": {ID: "outer", Start: "outer-start", End: "outer-end", Mode: "each", MaxIterations: 1000, Scope: []string{"outer-start", "inner-start", "body", "inner-end", "outer-end"}},
			"inner": {ID: "inner", Start: "inner-start", End: "inner-end", Mode: "each", MaxIterations: 1000, Scope: []string{"inner-start", "body", "inner-end"}, Parent: "outer", Depth: 1},
		},
	}
	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run nested each-item loop groups: %v", err)
	}
	want := []any{[]any{"A", "B"}, []any{}, []any{"C"}}
	if bodyCalls.Load() != 3 || !reflect.DeepEqual(run.Context.NodeResults["outer-end"], want) {
		t.Fatalf("unexpected nested each result: calls=%d output=%#v", bodyCalls.Load(), run.Context.NodeResults["outer-end"])
	}
	for _, groupID := range []string{"outer", "inner"} {
		for _, key := range []string{planning.LoopGroupItemVariable(groupID), planning.LoopGroupIndexVariable(groupID)} {
			if _, exists := run.Context.Variables[key]; exists {
				t.Fatalf("nested loop group leaked scoped variable %s", key)
			}
		}
	}
}

func TestDefaultScheduler_Run_BranchCondition(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("branch-source", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{
			Output: map[string]any{"kind": "left"},
		}, nil
	})
	local.Register("echo", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-branch",
		WorkflowID:        "wf-branch",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"branch", "left", "right"},
		Dependencies: map[string][]string{
			"branch": {},
			"left":   {"branch"},
			"right":  {"branch"},
		},
		Nodes: map[string]planning.PlanNode{
			"branch": {
				ID:           "branch",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "branch-source",
				Params:       map[string]any{"fn": "branch-source"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
			"left": {
				ID:           "left",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "echo",
				Input:        "go-left",
				Params:       map[string]any{"fn": "echo"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
			"right": {
				ID:           "right",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "echo",
				Input:        "go-right",
				Params:       map[string]any{"fn": "echo"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
		},
		Branches: map[string]planning.BranchMeta{
			"branch": {
				From: "branch",
				Mode: "first",
				Edges: []planning.BranchEdge{
					{To: "left", Condition: `Output.kind == "left"`},
					{To: "right", Condition: `Output.kind == "right"`},
				},
			},
		},
	}

	run, err := scheduler.Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected run status: %s", run.Status)
	}
	if got := run.Context.NodeResults["left"]; got != "go-left" {
		t.Fatalf("unexpected left result: %#v", got)
	}
	right := run.NodeRuns["right"]
	if right == nil || right.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected right node status: %+v", right)
	}
	if right.Metadata["skipped"] != true {
		t.Fatalf("expected right node to be skipped: %+v", right.Metadata)
	}
}

func TestDefaultDispatcherWaitsForConditionalDependencies(t *testing.T) {
	plan := &planning.ExecutionPlan{
		TopologicalOrder: []string{"menu", "data", "target"},
		Dependencies: map[string][]string{
			"menu":   {},
			"data":   {},
			"target": {"menu", "data"},
		},
		Branches: map[string]planning.BranchMeta{
			"menu": {
				From: "menu",
				Mode: "first",
				Edges: []planning.BranchEdge{
					{To: "target", Condition: `Output == "Option 1"`},
				},
			},
		},
	}
	run := wfruntime.NewWorkflowRun("run", "workflow", "version", "plan")
	run.NodeRuns = map[string]*wfruntime.NodeRun{
		"menu":   {NodeID: "menu", Status: wfruntime.StatusPending},
		"data":   {NodeID: "data", Status: wfruntime.StatusSuccess},
		"target": {NodeID: "target", Status: wfruntime.StatusPending},
	}

	if ready := NewDefaultDispatcher().Dispatch(plan, run); slices.Contains(ready, "target") {
		t.Fatalf("target became ready before its conditional dependency completed: %v", ready)
	}

	run.NodeRuns["menu"].Status = wfruntime.StatusSuccess
	run.NodeRuns["menu"].Result = "Option 1"
	run.Context.NodeResults["menu"] = "Option 1"
	if ready := NewDefaultDispatcher().Dispatch(plan, run); !slices.Contains(ready, "target") {
		t.Fatalf("target did not become ready after all dependencies completed: %v", ready)
	}
}

func TestDefaultScheduler_Run_MultiWayMenuBranch(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("menu-source", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: "Needs Review"}, nil
	})
	local.Register("menu-action", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.NodeID}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	node := func(id, ref string) planning.PlanNode {
		return planning.PlanNode{
			ID:           id,
			ExecutorType: string(executor.TypeLocalGo),
			ExecutorRef:  ref,
			Params:       map[string]any{"fn": ref},
			Retry:        planning.RetryPolicy{MaxAttempts: 1},
		}
	}
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-menu-branch",
		WorkflowID:        "wf-menu-branch",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"menu", "approve", "review", "reject"},
		Dependencies: map[string][]string{
			"menu":    {},
			"approve": {"menu"},
			"review":  {"menu"},
			"reject":  {"menu"},
		},
		Nodes: map[string]planning.PlanNode{
			"menu":    node("menu", "menu-source"),
			"approve": node("approve", "menu-action"),
			"review":  node("review", "menu-action"),
			"reject":  node("reject", "menu-action"),
		},
		Branches: map[string]planning.BranchMeta{
			"menu": {
				From: "menu",
				Mode: "first",
				Edges: []planning.BranchEdge{
					{To: "approve", Condition: `Output == "Approve"`, Priority: 0, Label: "Approve"},
					{To: "review", Condition: `Output == "Needs Review"`, Priority: 1, Label: "Needs Review"},
					{To: "reject", Condition: `Output == "Reject"`, Priority: 2, Label: "Reject"},
				},
			},
		},
	}

	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run menu branch: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected run status: %s", run.Status)
	}
	if got := run.Context.NodeResults["review"]; got != "review" {
		t.Fatalf("unexpected selected menu result: %#v", got)
	}
	for _, id := range []string{"approve", "reject"} {
		if nodeRun := run.NodeRuns[id]; nodeRun == nil || !nodeRunSkipped(nodeRun) {
			t.Fatalf("expected %s menu branch to be skipped: %+v", id, nodeRun)
		}
	}
}

func TestDefaultScheduler_Run_BranchSkipPropagatesAndJoins(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	executed := make([]string, 0, 4)
	local.Register("branch-source", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		executed = append(executed, req.NodeID)
		return executor.Result{Output: map[string]any{"kind": "left"}}, nil
	})
	local.Register("record", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		executed = append(executed, req.NodeID)
		return executor.Result{Output: req.NodeID}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	node := func(id, ref string) planning.PlanNode {
		return planning.PlanNode{
			ID:           id,
			ExecutorType: string(executor.TypeLocalGo),
			ExecutorRef:  ref,
			Params:       map[string]any{"fn": ref},
			Retry:        planning.RetryPolicy{MaxAttempts: 1},
		}
	}
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-branch-join",
		WorkflowID:        "wf-branch-join",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"branch", "left-1", "left-2", "right-1", "right-2", "join"},
		Dependencies: map[string][]string{
			"branch":  {},
			"left-1":  {"branch"},
			"left-2":  {"left-1"},
			"right-1": {"branch"},
			"right-2": {"right-1"},
			"join":    {"left-2", "right-2"},
		},
		Nodes: map[string]planning.PlanNode{
			"branch":  node("branch", "branch-source"),
			"left-1":  node("left-1", "record"),
			"left-2":  node("left-2", "record"),
			"right-1": node("right-1", "record"),
			"right-2": node("right-2", "record"),
			"join":    node("join", "record"),
		},
		Branches: map[string]planning.BranchMeta{
			"branch": {
				From: "branch",
				Mode: "first",
				Edges: []planning.BranchEdge{
					{To: "left-1", Condition: `Output.kind == "left"`, Priority: 0},
					{To: "right-1", Condition: `Output.kind == "right"`, Priority: 1},
				},
			},
		},
	}

	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run branch join: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected run status: %s", run.Status)
	}
	if got, want := strings.Join(executed, ","), "branch,left-1,left-2,join"; got != want {
		t.Fatalf("unexpected execution order: got %s want %s", got, want)
	}
	for _, id := range []string{"right-1", "right-2"} {
		nodeRun := run.NodeRuns[id]
		if nodeRun == nil || !nodeRunSkipped(nodeRun) {
			t.Fatalf("expected %s to be skipped: %+v", id, nodeRun)
		}
	}
	if nodeRunSkipped(run.NodeRuns["join"]) {
		t.Fatalf("join should execute when one branch is active: %+v", run.NodeRuns["join"])
	}
}

func TestDefaultScheduler_Run_SkippedNestedBranchPropagatesAndJoins(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	executed := make([]string, 0, 3)
	local.Register("branch-source", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		executed = append(executed, req.NodeID)
		kind := "otherwise"
		if req.NodeID == "nested-branch" {
			kind = "then"
		}
		return executor.Result{Output: map[string]any{"kind": kind}}, nil
	})
	local.Register("record", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		executed = append(executed, req.NodeID)
		return executor.Result{Output: req.NodeID}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	node := func(id, ref string) planning.PlanNode {
		return planning.PlanNode{
			ID:           id,
			ExecutorType: string(executor.TypeLocalGo),
			ExecutorRef:  ref,
			Params:       map[string]any{"fn": ref},
			Retry:        planning.RetryPolicy{MaxAttempts: 1},
		}
	}
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-nested-branch-skip",
		WorkflowID:        "wf-nested-branch-skip",
		WorkflowVersionID: "v1",
		TopologicalOrder: []string{
			"outer-branch",
			"nested-branch",
			"nested-then",
			"nested-otherwise",
			"nested-join",
			"outer-otherwise",
			"outer-join",
		},
		Dependencies: map[string][]string{
			"outer-branch":     {},
			"nested-branch":    {"outer-branch"},
			"nested-then":      {"nested-branch"},
			"nested-otherwise": {"nested-branch"},
			"nested-join":      {"nested-then", "nested-otherwise"},
			"outer-otherwise":  {"outer-branch"},
			"outer-join":       {"nested-join", "outer-otherwise"},
		},
		Nodes: map[string]planning.PlanNode{
			"outer-branch":     node("outer-branch", "branch-source"),
			"nested-branch":    node("nested-branch", "branch-source"),
			"nested-then":      node("nested-then", "record"),
			"nested-otherwise": node("nested-otherwise", "record"),
			"nested-join":      node("nested-join", "record"),
			"outer-otherwise":  node("outer-otherwise", "record"),
			"outer-join":       node("outer-join", "record"),
		},
		Branches: map[string]planning.BranchMeta{
			"outer-branch": {
				From: "outer-branch",
				Mode: "first",
				Edges: []planning.BranchEdge{
					{To: "nested-branch", Condition: `Output.kind == "then"`, Priority: 0},
					{To: "outer-otherwise", Condition: `Output.kind == "otherwise"`, Priority: 1},
				},
			},
			"nested-branch": {
				From: "nested-branch",
				Mode: "first",
				Edges: []planning.BranchEdge{
					{To: "nested-then", Condition: `Output.kind == "then"`, Priority: 0},
					{To: "nested-otherwise", Condition: `Output.kind == "otherwise"`, Priority: 1},
				},
			},
		},
	}

	run, err := NewDefaultScheduler(reg, wfruntime.NewMemoryStore()).Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run nested branch skip: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected run status: %s", run.Status)
	}
	if got, want := strings.Join(executed, ","), "outer-branch,outer-otherwise,outer-join"; got != want {
		t.Fatalf("unexpected execution order: got %s want %s", got, want)
	}
	for _, id := range []string{"nested-branch", "nested-then", "nested-otherwise", "nested-join"} {
		nodeRun := run.NodeRuns[id]
		if nodeRun == nil || !nodeRunSkipped(nodeRun) {
			t.Fatalf("expected %s to be skipped: %+v", id, nodeRun)
		}
	}
	if nodeRunSkipped(run.NodeRuns["outer-join"]) {
		t.Fatalf("outer join should execute when otherwise is active: %+v", run.NodeRuns["outer-join"])
	}
}

func TestDefaultScheduler_Run_BackEdgeLoop(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	var bodyCount atomic.Int32
	local.Register("body", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		current := bodyCount.Add(1)
		return executor.Result{
			Output: map[string]any{"count": current},
		}, nil
	})
	local.Register("check", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{
			Output: req.Context["body"],
		}, nil
	})
	local.Register("echo", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-back",
		WorkflowID:        "wf-back",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"start", "body", "check", "end"},
		Adjacency: map[string][]string{
			"start": {"body"},
			"body":  {"check"},
			"check": {"end"},
			"end":   {},
		},
		Dependencies: map[string][]string{
			"start": {},
			"body":  {"start"},
			"check": {"body"},
			"end":   {"check"},
		},
		Nodes: map[string]planning.PlanNode{
			"start": {
				ID:           "start",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "echo",
				Input:        "go",
				Params:       map[string]any{"fn": "echo"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
			"body": {
				ID:           "body",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "body",
				Params:       map[string]any{"fn": "body"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
			"check": {
				ID:           "check",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "check",
				Params:       map[string]any{"fn": "check"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
			"end": {
				ID:           "end",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "echo",
				Input:        "done",
				Params:       map[string]any{"fn": "echo"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
		},
		BackEdges: map[string]planning.BranchMeta{
			"check": {
				From: "check",
				Mode: "first",
				Edges: []planning.BranchEdge{
					{To: "body", Condition: `Output.count < 3`},
				},
			},
		},
	}

	run, err := scheduler.Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected run status: %s", run.Status)
	}
	if got := bodyCount.Load(); got != 3 {
		t.Fatalf("unexpected body execution count: %d", got)
	}
	if got := run.Context.NodeResults["end"]; got != "done" {
		t.Fatalf("unexpected end result: %#v", got)
	}
}

func TestDefaultScheduler_Run_InputBindingReplace(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("source", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{
			Output: map[string]any{
				"message": "hello-binding",
			},
		}, nil
	})
	local.Register("echo", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-input-replace",
		WorkflowID:        "wf-input-replace",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"a", "b"},
		Dependencies: map[string][]string{
			"a": {},
			"b": {"a"},
		},
		Nodes: map[string]planning.PlanNode{
			"a": {
				ID:           "a",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "source",
				Params:       map[string]any{"fn": "source"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
			"b": {
				ID:           "b",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "echo",
				Params:       map[string]any{"fn": "echo"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
				InputSpec: &planning.InputSpec{
					Mode: "replace",
					Bindings: []planning.InputBinding{
						{From: "a", Path: "message", Required: true},
					},
				},
			},
		},
	}

	run, err := scheduler.Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got := run.Context.NodeResults["b"]; got != "hello-binding" {
		t.Fatalf("unexpected bound input result: %#v", got)
	}
}

func TestDefaultScheduler_Run_ParameterBindingsOverrideStaticParams(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("capture-params", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: map[string]any{
			"duration_ms": req.Params["duration_ms"],
			"enabled":     req.Params["enabled"],
		}}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-param-bindings",
		WorkflowID:        "wf-param-bindings",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"capture"},
		Dependencies:      map[string][]string{"capture": {}},
		Nodes: map[string]planning.PlanNode{
			"capture": {
				ID:           "capture",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "capture-params",
				Params: map[string]any{
					"fn":          "capture-params",
					"duration_ms": 1000,
					"enabled":     false,
				},
				ParamBindings: map[string]planning.InputBinding{
					"duration_ms": {Source: "var", From: "wait_ms", Required: true, Transform: "int(Value)"},
					"enabled":     {Source: "var", From: "enabled", Required: true},
				},
				Retry: planning.RetryPolicy{MaxAttempts: 1},
			},
		},
	}
	run := wfruntime.NewWorkflowRun("run-param-bindings", "wf-param-bindings", "v1", "plan-param-bindings")
	run.Context.Variables["wait_ms"] = float64(25)
	run.Context.Variables["enabled"] = true

	resultRun, err := scheduler.Run(context.Background(), plan, run)
	if err != nil {
		t.Fatalf("run parameter bindings: %v", err)
	}
	result, ok := resultRun.Context.NodeResults["capture"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %T", resultRun.Context.NodeResults["capture"])
	}
	if result["duration_ms"] != 25 || result["enabled"] != true {
		t.Fatalf("unexpected resolved params: %#v", result)
	}
}

func TestDefaultScheduler_Run_ParameterTemplateCombinesTextAndBindings(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("template-source", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: map[string]any{
			"name":    "Alice",
			"score":   98,
			"profile": map[string]any{"active": true},
		}}, nil
	})
	local.Register("capture-template", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Params["message"]}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-param-template",
		WorkflowID:        "wf-param-template",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"source", "capture"},
		Dependencies: map[string][]string{
			"source":  {},
			"capture": {"source"},
		},
		Nodes: map[string]planning.PlanNode{
			"source": {
				ID:           "source",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "template-source",
				Params:       map[string]any{"fn": "template-source"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
			"capture": {
				ID:           "capture",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "capture-template",
				Params:       map[string]any{"fn": "capture-template"},
				ParamTemplates: map[string]planning.ParamTemplate{
					"message": {Segments: []planning.ParamTemplateSegment{
						{Type: "text", Value: "Hello "},
						{Type: "binding", Binding: &planning.InputBinding{Source: "node", From: "source", Path: "name", Required: true}},
						{Type: "text", Value: ", score "},
						{Type: "binding", Binding: &planning.InputBinding{Source: "node", From: "source", Path: "score", Required: true}},
						{Type: "text", Value: "; profile "},
						{Type: "binding", Binding: &planning.InputBinding{Source: "node", From: "source", Path: "profile", Required: true}},
					}},
				},
				Retry: planning.RetryPolicy{MaxAttempts: 1},
			},
		},
	}

	run, err := scheduler.Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run parameter template: %v", err)
	}
	if got := run.Context.NodeResults["capture"]; got != `Hello Alice, score 98; profile {"active":true}` {
		t.Fatalf("unexpected parameter template result: %#v", got)
	}
}

func TestDefaultScheduler_Run_InputBindingObject(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("left", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{
			Output: map[string]any{"name": "alice"},
		}, nil
	})
	local.Register("right", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{
			Output: map[string]any{"score": 98},
		}, nil
	})
	local.Register("echo", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-input-object",
		WorkflowID:        "wf-input-object",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"a", "b", "c"},
		Dependencies: map[string][]string{
			"a": {},
			"b": {},
			"c": {"a", "b"},
		},
		Nodes: map[string]planning.PlanNode{
			"a": {
				ID:           "a",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "left",
				Params:       map[string]any{"fn": "left"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
			"b": {
				ID:           "b",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "right",
				Params:       map[string]any{"fn": "right"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
			"c": {
				ID:           "c",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "echo",
				Params:       map[string]any{"fn": "echo"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
				Input: map[string]any{
					"kind": "summary",
				},
				InputSpec: &planning.InputSpec{
					Mode: "object",
					Bindings: []planning.InputBinding{
						{From: "a", Path: "name", As: "user", Required: true},
						{From: "b", Path: "score", As: "score", Required: true},
					},
				},
			},
		},
	}

	run, err := scheduler.Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	result, ok := run.Context.NodeResults["c"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %T", run.Context.NodeResults["c"])
	}
	if result["kind"] != "summary" || result["user"] != "alice" || result["score"] != 98 {
		t.Fatalf("unexpected object-bound input result: %#v", result)
	}
}

func TestDefaultScheduler_Run_InputBindingRequiredPathMissing(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("source", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{
			Output: map[string]any{"message": "hello"},
		}, nil
	})
	local.Register("echo", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-input-required",
		WorkflowID:        "wf-input-required",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"a", "b"},
		Dependencies: map[string][]string{
			"a": {},
			"b": {"a"},
		},
		Nodes: map[string]planning.PlanNode{
			"a": {
				ID:           "a",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "source",
				Params:       map[string]any{"fn": "source"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
			"b": {
				ID:           "b",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "echo",
				Params:       map[string]any{"fn": "echo"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
				InputSpec: &planning.InputSpec{
					Mode: "replace",
					Bindings: []planning.InputBinding{
						{From: "a", Path: "missing", Required: true},
					},
				},
			},
		},
	}

	run, err := scheduler.Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("unexpected scheduler error: %v", err)
	}
	if run == nil || run.Status != wfruntime.StatusFailed {
		t.Fatalf("unexpected run state: %+v", run)
	}
	if node := run.NodeRuns["b"]; node == nil || node.Status != wfruntime.StatusFailed {
		t.Fatalf("unexpected node state: %+v", node)
	}
}

func TestDefaultScheduler_Run_InputBindingDefaultAndTransform(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("source", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{
			Output: map[string]any{
				"name":    "alice",
				"count":   "12.5",
				"profile": map[string]any{"name": "alice"},
			},
		}, nil
	})
	local.Register("echo", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-input-default-transform",
		WorkflowID:        "wf-input-default-transform",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"a", "b"},
		Dependencies: map[string][]string{
			"a": {},
			"b": {"a"},
		},
		Nodes: map[string]planning.PlanNode{
			"a": {
				ID:           "a",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "source",
				Params:       map[string]any{"fn": "source"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
			},
			"b": {
				ID:           "b",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "echo",
				Params:       map[string]any{"fn": "echo"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
				Input: map[string]any{
					"kind": "profile",
				},
				InputSpec: &planning.InputSpec{
					Mode: "object",
					Bindings: []planning.InputBinding{
						{From: "a", Path: "name", As: "name", Required: true, Transform: `Value + "-vip"`},
						{From: "a", Path: "count", As: "count_text", Required: true, Transform: `string(Value)`},
						{From: "a", Path: "count", As: "count_number", Required: true, Transform: `float(Value)`},
						{From: "a", Path: "profile", As: "profile_json", Required: true, Transform: `toJSON(Value)`},
						{From: "a", Path: "missing", As: "title", Default: "guest"},
					},
				},
			},
		},
	}

	run, err := scheduler.Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	result, ok := run.Context.NodeResults["b"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %T", run.Context.NodeResults["b"])
	}
	if result["kind"] != "profile" ||
		result["name"] != "alice-vip" ||
		result["count_text"] != "12.5" ||
		result["count_number"] != 12.5 ||
		result["profile_json"] != "{\n  \"name\": \"alice\"\n}" ||
		result["title"] != "guest" {
		t.Fatalf("unexpected default/transform result: %#v", result)
	}
}

func TestDefaultScheduler_Run_InputBindingVarRequestRunSources(t *testing.T) {
	reg := executor.NewRegistry()
	local := executor.NewLocalExecutor()
	local.Register("echo", func(ctx context.Context, req executor.Request) (executor.Result, error) {
		return executor.Result{Output: req.Input}, nil
	})
	if err := reg.Register(local); err != nil {
		t.Fatalf("register local executor: %v", err)
	}

	scheduler := NewDefaultScheduler(reg, wfruntime.NewMemoryStore())
	plan := &planning.ExecutionPlan{
		PlanID:            "plan-input-sources",
		WorkflowID:        "wf-input-sources",
		WorkflowVersionID: "v1",
		TopologicalOrder:  []string{"summary"},
		Dependencies: map[string][]string{
			"summary": {},
		},
		Nodes: map[string]planning.PlanNode{
			"summary": {
				ID:           "summary",
				ExecutorType: string(executor.TypeLocalGo),
				ExecutorRef:  "echo",
				Params:       map[string]any{"fn": "echo"},
				Retry:        planning.RetryPolicy{MaxAttempts: 1},
				InputSpec: &planning.InputSpec{
					Mode: "object",
					Bindings: []planning.InputBinding{
						{Source: "var", From: "tenant_id", As: "tenant_id", Required: true},
						{Source: "request", From: "body.message", As: "message", Required: true},
						{Source: "run", From: "workflow_id", As: "workflow_id", Required: true},
					},
				},
			},
		},
	}

	run := wfruntime.NewWorkflowRun("run-source-1", "wf-input-sources", "v1", "plan-input-sources")
	run.Context.Variables["tenant_id"] = "t-001"
	run.Context.Variables["request"] = map[string]any{
		"body": map[string]any{
			"message": "hello-request",
		},
	}

	resultRun, err := scheduler.Run(context.Background(), plan, run)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	result, ok := resultRun.Context.NodeResults["summary"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %T", resultRun.Context.NodeResults["summary"])
	}
	if result["tenant_id"] != "t-001" || result["message"] != "hello-request" || result["workflow_id"] != "wf-input-sources" {
		t.Fatalf("unexpected source-bound result: %#v", result)
	}
}
