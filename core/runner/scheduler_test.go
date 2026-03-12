package runner

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/planning"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
)

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
	if run.Context.NodeResults["b"] != "world" {
		t.Fatalf("unexpected node result: %#v", run.Context.NodeResults)
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
			Output: map[string]any{"name": "alice"},
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
	if result["kind"] != "profile" || result["name"] != "alice-vip" || result["title"] != "guest" {
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
