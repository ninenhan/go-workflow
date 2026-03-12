package planning

import (
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
)

func TestCompilerCompile(t *testing.T) {
	compiler := NewCompiler()
	version := &definition.WorkflowVersion{
		ID:         "v1",
		WorkflowID: "wf-1",
		Version:    1,
		Definition: &definition.WorkflowDefinition{
			ID:   "wf-1",
			Name: "demo",
			Nodes: []definition.Node{
				{ID: "start", Name: "start", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeLocalGo, Ref: "start.fn"}, Retry: definition.RetryPolicy{MaxAttempts: 1}},
				{ID: "end", Name: "end", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeLocalGo, Ref: "end.fn"}, Timeout: time.Second},
			},
			Edges: []definition.Edge{{From: "start", To: "end"}},
		},
	}

	plan, err := compiler.Compile(version)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if plan.PlanID == "" {
		t.Fatalf("empty plan id")
	}
	if len(plan.EntryNodes) != 1 || plan.EntryNodes[0] != "start" {
		t.Fatalf("unexpected entry nodes: %#v", plan.EntryNodes)
	}
	if len(plan.TopologicalOrder) != 2 {
		t.Fatalf("unexpected topological order: %#v", plan.TopologicalOrder)
	}
}

func TestCompilerCompile_BackEdge(t *testing.T) {
	compiler := NewCompiler()
	version := &definition.WorkflowVersion{
		ID:         "v2",
		WorkflowID: "wf-loop",
		Version:    1,
		Definition: &definition.WorkflowDefinition{
			ID:   "wf-loop",
			Name: "loop",
			Nodes: []definition.Node{
				{ID: "start", Name: "start", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeLocalGo, Ref: "start.fn"}},
				{ID: "body", Name: "body", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeLocalGo, Ref: "body.fn"}},
				{ID: "check", Name: "check", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeLocalGo, Ref: "check.fn"}},
				{ID: "end", Name: "end", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeLocalGo, Ref: "end.fn"}},
			},
			Edges: []definition.Edge{
				{From: "start", To: "body"},
				{From: "body", To: "check"},
				{From: "check", To: "end"},
				{From: "check", To: "body", Kind: definition.EdgeKindBack, Condition: "Output.count < 3"},
			},
		},
	}

	plan, err := compiler.Compile(version)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(plan.TopologicalOrder) != 4 {
		t.Fatalf("unexpected topo order: %#v", plan.TopologicalOrder)
	}
	meta, ok := plan.BackEdges["check"]
	if !ok || len(meta.Edges) != 1 || meta.Edges[0].To != "body" {
		t.Fatalf("unexpected back edge plan: %#v", plan.BackEdges)
	}
}

func TestCompilerCompile_InputBindingRequiresDirectDependency(t *testing.T) {
	compiler := NewCompiler()
	version := &definition.WorkflowVersion{
		ID:         "v3",
		WorkflowID: "wf-bind",
		Version:    1,
		Definition: &definition.WorkflowDefinition{
			ID:   "wf-bind",
			Name: "bind",
			Nodes: []definition.Node{
				{ID: "a", Name: "a", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeLocalGo, Ref: "a.fn"}},
				{ID: "b", Name: "b", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeLocalGo, Ref: "b.fn"}},
				{
					ID:       "c",
					Name:     "c",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeLocalGo, Ref: "c.fn"},
					InputSpec: &definition.InputSpec{
						Bindings: []definition.InputBinding{
							{From: "a", Required: true},
						},
					},
				},
			},
			Edges: []definition.Edge{
				{From: "b", To: "c"},
			},
		},
	}

	if _, err := compiler.Compile(version); err == nil {
		t.Fatalf("expected compile failure for non-direct dependency binding")
	}
}
