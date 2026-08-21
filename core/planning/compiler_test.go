package planning

import (
	"fmt"
	"strings"
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
				{ID: "start", Name: "start", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeLocalGo, Ref: "start.fn"}, Retry: &definition.RetryPolicy{MaxAttempts: 1}},
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

func TestCompilerCompileRejectsInvalidLoopBounds(t *testing.T) {
	for _, maxIterations := range []int{0, 1, MaxNodeLoopIterations + 1} {
		t.Run(fmt.Sprintf("max_%d", maxIterations), func(t *testing.T) {
			compiler := NewCompiler()
			_, err := compiler.Compile(&definition.WorkflowVersion{
				ID:         "v-loop-bounds",
				WorkflowID: "wf-loop-bounds",
				Version:    1,
				Definition: &definition.WorkflowDefinition{
					ID: "wf-loop-bounds",
					Nodes: []definition.Node{{
						ID:       "repeat",
						Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
						Loop:     &definition.LoopPolicy{MaxIterations: maxIterations},
					}},
				},
			})
			if err == nil {
				t.Fatalf("expected loop bound %d to be rejected", maxIterations)
			}
		})
	}
}

func TestCompilerCompileValidatesRetryPolicyBounds(t *testing.T) {
	tests := []struct {
		name   string
		policy definition.RetryPolicy
		err    string
	}{
		{
			name:   "supported boundary",
			policy: definition.RetryPolicy{MaxAttempts: MaxNodeRetryAttempts, Backoff: time.Second, MaxBackoff: MaxNodeRetryBackoff},
		},
		{
			name:   "too many attempts",
			policy: definition.RetryPolicy{MaxAttempts: MaxNodeRetryAttempts + 1},
			err:    "max_attempts",
		},
		{
			name:   "negative backoff",
			policy: definition.RetryPolicy{MaxAttempts: 2, Backoff: -time.Second},
			err:    "retry backoff",
		},
		{
			name:   "backoff too large",
			policy: definition.RetryPolicy{MaxAttempts: 2, Backoff: MaxNodeRetryBackoff + time.Nanosecond},
			err:    "retry backoff",
		},
		{
			name:   "maximum below base",
			policy: definition.RetryPolicy{MaxAttempts: 2, Backoff: 2 * time.Second, MaxBackoff: time.Second},
			err:    "max_backoff cannot be less",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewCompiler().Compile(&definition.WorkflowVersion{
				ID:         "v-retry-policy",
				WorkflowID: "wf-retry-policy",
				Version:    1,
				Definition: &definition.WorkflowDefinition{
					ID: "wf-retry-policy",
					Nodes: []definition.Node{{
						ID:       "request",
						Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "HttpUnit"},
						Retry:    &test.policy,
					}},
				},
			})
			if test.err == "" {
				if err != nil {
					t.Fatalf("compile valid retry policy: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("error = %v, want %q", err, test.err)
			}
		})
	}
}

func TestCompilerCompileValidatesEachItemLoop(t *testing.T) {
	tests := []struct {
		name string
		loop definition.LoopPolicy
		err  string
	}{
		{name: "supported", loop: definition.LoopPolicy{Mode: "each", MaxIterations: 1000}},
		{name: "unknown mode", loop: definition.LoopPolicy{Mode: "parallel", MaxIterations: 10}, err: "mode must be count or each"},
		{name: "condition", loop: definition.LoopPolicy{Mode: "each", MaxIterations: 10, Condition: "Output != nil"}, err: "cannot define a condition"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := NewCompiler().Compile(&definition.WorkflowVersion{
				ID:         "v-each",
				WorkflowID: "wf-each",
				Definition: &definition.WorkflowDefinition{
					ID: "wf-each",
					Nodes: []definition.Node{{
						ID:       "each",
						Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
						Loop:     &test.loop,
					}},
				},
			})
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("expected %q, got %v", test.err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("compile each-item loop: %v", err)
			}
			if plan.Nodes["each"].Loop == nil || plan.Nodes["each"].Loop.Mode != "each" {
				t.Fatalf("each-item mode was not preserved: %#v", plan.Nodes["each"].Loop)
			}
		})
	}
}

func TestCompilerCompileDynamicLoopCount(t *testing.T) {
	countBinding := definition.InputBinding{
		Source:   definition.InputSourceVar,
		From:     "repeat_count",
		Required: true,
	}
	plan, err := NewCompiler().Compile(&definition.WorkflowVersion{
		ID:         "v-dynamic-loop-count",
		WorkflowID: "wf-dynamic-loop-count",
		Definition: &definition.WorkflowDefinition{
			ID: "wf-dynamic-loop-count",
			Nodes: []definition.Node{{
				ID:       "repeat",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
				Loop: &definition.LoopPolicy{
					Mode:          "count",
					MaxIterations: 1000,
					CountBinding:  &countBinding,
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("compile dynamic loop count: %v", err)
	}
	if got := plan.Nodes["repeat"].Loop.CountBinding; got == nil || got.Source != string(definition.InputSourceVar) || got.From != "repeat_count" {
		t.Fatalf("dynamic count binding was not preserved: %#v", got)
	}
}

func TestCompilerCompileRejectsInvalidDynamicLoopCountBindings(t *testing.T) {
	variableBinding := definition.InputBinding{
		Source:   definition.InputSourceVar,
		From:     "repeat_count",
		Required: true,
	}
	eachLoop := definition.LoopPolicy{
		Mode:          "each",
		MaxIterations: 1000,
		CountBinding:  &variableBinding,
	}
	_, err := NewCompiler().Compile(&definition.WorkflowVersion{
		ID:         "v-each-count-binding",
		WorkflowID: "wf-each-count-binding",
		Definition: &definition.WorkflowDefinition{
			ID: "wf-each-count-binding",
			Nodes: []definition.Node{{
				ID:       "repeat",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
				Loop:     &eachLoop,
			}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot define count_binding") {
		t.Fatalf("expected each-item loop count binding rejection, got %v", err)
	}

	nodeBinding := definition.InputBinding{
		Source:   definition.InputSourceNode,
		From:     "source",
		Required: true,
	}
	_, err = NewCompiler().Compile(&definition.WorkflowVersion{
		ID:         "v-indirect-count-binding",
		WorkflowID: "wf-indirect-count-binding",
		Definition: &definition.WorkflowDefinition{
			ID: "wf-indirect-count-binding",
			Nodes: []definition.Node{
				{ID: "source", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"}},
				{ID: "middle", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"}},
				{
					ID:       "repeat",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
					Loop: &definition.LoopPolicy{
						Mode:          "count",
						MaxIterations: 1000,
						CountBinding:  &nodeBinding,
					},
				},
			},
			Edges: []definition.Edge{{From: "source", To: "middle"}, {From: "middle", To: "repeat"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not a direct dependency") {
		t.Fatalf("expected indirect count binding rejection, got %v", err)
	}
}

func TestCompilerCompileLoopGroup(t *testing.T) {
	definitionValue := &definition.WorkflowDefinition{
		ID:         "wf-group",
		EntryNodes: []string{"before"},
		Nodes: []definition.Node{
			{ID: "before", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "start", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "body", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "end", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "after", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
		},
		Edges: []definition.Edge{
			{From: "before", To: "start"},
			{From: "start", To: "body"},
			{From: "body", To: "end"},
			{From: "end", To: "after"},
		},
		LoopGroups: []definition.LoopGroup{{ID: "group", Start: "start", End: "end", Mode: "each", MaxIterations: 1000}},
	}
	plan, err := NewCompiler().Compile(&definition.WorkflowVersion{ID: "v1", WorkflowID: "wf-group", Definition: definitionValue})
	if err != nil {
		t.Fatalf("compile loop group: %v", err)
	}
	group := plan.LoopGroups["group"]
	if group.Start != "start" || group.End != "end" || strings.Join(group.Scope, ",") != "start,body,end" {
		t.Fatalf("unexpected loop group: %#v", group)
	}
}

func TestCompilerCompileDynamicLoopGroupCount(t *testing.T) {
	countBinding := definition.InputBinding{
		Source:   definition.InputSourceVar,
		From:     "repeat_count",
		Required: true,
	}
	value := &definition.WorkflowDefinition{
		ID: "wf-dynamic-group-count",
		Nodes: []definition.Node{
			{ID: "start", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "end", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
		},
		Edges: []definition.Edge{{From: "start", To: "end"}},
		LoopGroups: []definition.LoopGroup{{
			ID:            "group",
			Start:         "start",
			End:           "end",
			Mode:          "count",
			MaxIterations: 1000,
			CountBinding:  &countBinding,
		}},
	}
	plan, err := NewCompiler().Compile(&definition.WorkflowVersion{
		ID:         "v-dynamic-group-count",
		WorkflowID: value.ID,
		Definition: value,
	})
	if err != nil {
		t.Fatalf("compile dynamic loop group count: %v", err)
	}
	if got := plan.LoopGroups["group"].CountBinding; got == nil || got.Source != string(definition.InputSourceVar) || got.From != "repeat_count" {
		t.Fatalf("dynamic group count binding was not preserved: %#v", got)
	}

	value.LoopGroups[0].Mode = "each"
	_, err = NewCompiler().Compile(&definition.WorkflowVersion{
		ID:         "v-each-group-count-binding",
		WorkflowID: value.ID,
		Definition: value,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot define count_binding") {
		t.Fatalf("expected each-item group count binding rejection, got %v", err)
	}
}

func TestCompilerCompileLoopGroupAllowsReadOnlyExternalBindings(t *testing.T) {
	build := func(withFlowEdge bool) *definition.WorkflowDefinition {
		edges := []definition.Edge{
			{From: "source", To: "start"},
			{From: "start", To: "body"},
			{From: "body", To: "end"},
		}
		if withFlowEdge {
			edges = append(edges, definition.Edge{From: "source", To: "body"})
		}
		return &definition.WorkflowDefinition{
			ID:         "wf-group-external-binding",
			EntryNodes: []string{"source"},
			Nodes: []definition.Node{
				{ID: "source", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"}},
				{ID: "start", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"}},
				{
					ID:        "body",
					DependsOn: []string{"source"},
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "ReplaceTextUnit"},
					ParamBindings: map[string]definition.InputBinding{
						"replacement": {Source: definition.InputSourceNode, From: "source", Required: true},
					},
				},
				{ID: "end", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"}},
			},
			Edges:      edges,
			LoopGroups: []definition.LoopGroup{{ID: "group", Start: "start", End: "end", Mode: "count", MaxIterations: 3}},
		}
	}

	plan, err := NewCompiler().Compile(&definition.WorkflowVersion{
		ID:         "v-external-binding",
		WorkflowID: "wf-group-external-binding",
		Definition: build(false),
	})
	if err != nil {
		t.Fatalf("compile read-only external loop binding: %v", err)
	}
	if got := strings.Join(plan.LoopGroups["group"].Scope, ","); got != "start,body,end" {
		t.Fatalf("unexpected loop scope: %s", got)
	}
	if got := strings.Join(plan.Dependencies["body"], ","); !strings.Contains(got, "source") {
		t.Fatalf("external binding dependency was not preserved: %s", got)
	}

	_, err = NewCompiler().Compile(&definition.WorkflowVersion{
		ID:         "v-external-flow",
		WorkflowID: "wf-group-external-flow",
		Definition: build(true),
	})
	if err == nil || !strings.Contains(err.Error(), "external input") {
		t.Fatalf("expected external flow edge to remain invalid, got %v", err)
	}
}

func TestCompilerCompileValidatesLoopGroupVariables(t *testing.T) {
	base := func(mode string) *definition.WorkflowDefinition {
		return &definition.WorkflowDefinition{
			ID: "wf-repeat-variables",
			Nodes: []definition.Node{
				{
					ID: "start", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
					ParamBindings: map[string]definition.InputBinding{
						"iteration": {Source: definition.InputSourceVar, From: LoopGroupIndexVariable("group"), Required: true},
					},
				},
				{
					ID: "body", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
					InputSpec: &definition.InputSpec{Mode: definition.InputModeReplace, Bindings: []definition.InputBinding{
						{Source: definition.InputSourceVar, From: LoopGroupItemVariable("group"), Required: true},
					}},
				},
				{ID: "end", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
				{ID: "after", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			},
			Edges:      []definition.Edge{{From: "start", To: "body"}, {From: "body", To: "end"}, {From: "end", To: "after"}},
			LoopGroups: []definition.LoopGroup{{ID: "group", Start: "start", End: "end", Mode: mode, MaxIterations: 3}},
		}
	}
	compile := func(value *definition.WorkflowDefinition) error {
		_, err := NewCompiler().Compile(&definition.WorkflowVersion{ID: "v1", WorkflowID: value.ID, Definition: value})
		return err
	}
	if err := compile(base("each")); err != nil {
		t.Fatalf("compile valid repeat variables: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*definition.WorkflowDefinition)
		want   string
	}{
		{
			name: "outside scope",
			mutate: func(value *definition.WorkflowDefinition) {
				value.Nodes[3].ParamBindings = map[string]definition.InputBinding{
					"message": {Source: definition.InputSourceVar, From: LoopGroupItemVariable("group")},
				}
			},
			want: "outside its scope",
		},
		{
			name: "unknown group",
			mutate: func(value *definition.WorkflowDefinition) {
				value.Nodes[1].InputSpec.Bindings[0].From = LoopGroupItemVariable("missing")
			},
			want: "unknown repeat group missing",
		},
		{
			name: "item from count group",
			mutate: func(value *definition.WorkflowDefinition) {
				value.LoopGroups[0].Mode = "count"
			},
			want: "Repeat Item from count loop group group",
		},
		{
			name: "start input self reference",
			mutate: func(value *definition.WorkflowDefinition) {
				value.Nodes[0].InputSpec = &definition.InputSpec{Mode: definition.InputModeReplace, Bindings: []definition.InputBinding{
					{Source: definition.InputSourceVar, From: LoopGroupIndexVariable("group")},
				}}
			},
			want: "before the iteration starts",
		},
		{
			name: "malformed reserved variable",
			mutate: func(value *definition.WorkflowDefinition) {
				value.Nodes[1].InputSpec.Bindings[0].From = "__loop_group.group.value"
			},
			want: "malformed repeat variable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base("each")
			test.mutate(value)
			err := compile(value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestCompilerCompileNestedLoopGroups(t *testing.T) {
	definitionValue := &definition.WorkflowDefinition{
		ID:         "wf-nested-groups",
		EntryNodes: []string{"before"},
		Nodes: []definition.Node{
			{ID: "before", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "outer-start", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "inner-start", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "body", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "inner-end", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "outer-end", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "after", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
		},
		Edges: []definition.Edge{
			{From: "before", To: "outer-start"},
			{From: "outer-start", To: "inner-start"},
			{From: "inner-start", To: "body"},
			{From: "body", To: "inner-end"},
			{From: "inner-end", To: "outer-end"},
			{From: "outer-end", To: "after"},
		},
		LoopGroups: []definition.LoopGroup{
			{ID: "inner", Start: "inner-start", End: "inner-end", Mode: "each", MaxIterations: 1000},
			{ID: "outer", Start: "outer-start", End: "outer-end", Mode: "count", MaxIterations: 2},
		},
	}
	plan, err := NewCompiler().Compile(&definition.WorkflowVersion{ID: "v1", WorkflowID: definitionValue.ID, Definition: definitionValue})
	if err != nil {
		t.Fatalf("compile nested loop groups: %v", err)
	}
	inner := plan.LoopGroups["inner"]
	outer := plan.LoopGroups["outer"]
	if inner.Parent != "outer" || inner.Depth != 1 {
		t.Fatalf("unexpected inner loop hierarchy: %#v", inner)
	}
	if outer.Parent != "" || outer.Depth != 0 {
		t.Fatalf("unexpected outer loop hierarchy: %#v", outer)
	}
}

func TestCompilerCompileRejectsInvalidLoopGroupOverlap(t *testing.T) {
	base := func(groups []definition.LoopGroup) *definition.WorkflowDefinition {
		return &definition.WorkflowDefinition{
			ID: "wf-overlapping-groups",
			Nodes: []definition.Node{
				{ID: "a", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
				{ID: "b", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
				{ID: "c", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
				{ID: "d", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			},
			Edges:      []definition.Edge{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "d"}},
			LoopGroups: groups,
		}
	}
	tests := []struct {
		name   string
		groups []definition.LoopGroup
		want   string
	}{
		{
			name: "same scope",
			groups: []definition.LoopGroup{
				{ID: "one", Start: "a", End: "d", MaxIterations: 2},
				{ID: "two", Start: "a", End: "d", MaxIterations: 3},
			},
			want: "same scope",
		},
		{
			name: "partial overlap",
			groups: []definition.LoopGroup{
				{ID: "left", Start: "a", End: "c", MaxIterations: 2},
				{ID: "right", Start: "b", End: "d", MaxIterations: 2},
			},
			want: "partially overlap",
		},
		{
			name: "shared boundary",
			groups: []definition.LoopGroup{
				{ID: "outer", Start: "a", End: "d", MaxIterations: 2},
				{ID: "inner", Start: "a", End: "c", MaxIterations: 2},
			},
			want: "cannot share a boundary node",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewCompiler().Compile(&definition.WorkflowVersion{ID: "v1", WorkflowID: "wf-overlap", Definition: base(test.groups)})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestCompilerCompileRejectsUnstructuredLoopGroup(t *testing.T) {
	definitionValue := &definition.WorkflowDefinition{
		ID: "wf-group-invalid",
		Nodes: []definition.Node{
			{ID: "start", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "body", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "end", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			{ID: "leak", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
		},
		Edges: []definition.Edge{
			{From: "start", To: "body"},
			{From: "body", To: "end"},
			{From: "body", To: "leak"},
		},
		LoopGroups: []definition.LoopGroup{{ID: "group", Start: "start", End: "end", MaxIterations: 3}},
	}
	_, err := NewCompiler().Compile(&definition.WorkflowVersion{ID: "v1", WorkflowID: "wf-group-invalid", Definition: definitionValue})
	if err == nil || !strings.Contains(err.Error(), "external output") {
		t.Fatalf("expected unstructured loop group error, got %v", err)
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

func TestCompilerCompile_ParameterBinding(t *testing.T) {
	compiler := NewCompiler()
	plan, err := compiler.Compile(&definition.WorkflowVersion{
		ID:         "v-param-binding",
		WorkflowID: "wf-param-binding",
		Version:    1,
		Definition: &definition.WorkflowDefinition{
			ID: "wf-param-binding",
			Nodes: []definition.Node{{
				ID:       "wait",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TimeoutUnit"},
				Params:   map[string]any{"duration_ms": 1000},
				ParamBindings: map[string]definition.InputBinding{
					"duration_ms": {Source: definition.InputSourceVar, From: "wait_ms", Label: "Delay", Required: true},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("compile parameter binding: %v", err)
	}
	binding, ok := plan.Nodes["wait"].ParamBindings["duration_ms"]
	if !ok || binding.Source != "var" || binding.From != "wait_ms" || binding.Label != "Delay" || !binding.Required {
		t.Fatalf("unexpected parameter binding: %#v", plan.Nodes["wait"].ParamBindings)
	}
}

func TestCompilerCompile_ParameterTemplate(t *testing.T) {
	compiler := NewCompiler()
	plan, err := compiler.Compile(&definition.WorkflowVersion{
		ID:         "v-param-template",
		WorkflowID: "wf-param-template",
		Version:    1,
		Definition: &definition.WorkflowDefinition{
			ID: "wf-param-template",
			Nodes: []definition.Node{
				{ID: "source", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "RemarkUnit"}},
				{
					ID:       "target",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
					ParamTemplates: map[string]definition.ParamTemplate{
						"message": {Segments: []definition.ParamTemplateSegment{
							{Type: "text", Value: "Hello "},
							{Type: "binding", Binding: &definition.InputBinding{Source: definition.InputSourceNode, From: "source", Path: "name", Required: true}},
						}},
					},
				},
			},
			Edges: []definition.Edge{{From: "source", To: "target"}},
		},
	})
	if err != nil {
		t.Fatalf("compile parameter template: %v", err)
	}
	template := plan.Nodes["target"].ParamTemplates["message"]
	if len(template.Segments) != 2 || template.Segments[1].Binding == nil || template.Segments[1].Binding.From != "source" {
		t.Fatalf("unexpected parameter template: %#v", template)
	}
}

func TestCompilerCompile_RejectsParameterBindingTemplateConflict(t *testing.T) {
	compiler := NewCompiler()
	_, err := compiler.Compile(&definition.WorkflowVersion{
		ID:         "v-param-template-conflict",
		WorkflowID: "wf-param-template-conflict",
		Version:    1,
		Definition: &definition.WorkflowDefinition{
			ID: "wf-param-template-conflict",
			Nodes: []definition.Node{{
				ID:       "target",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
				ParamBindings: map[string]definition.InputBinding{
					"message": {Source: definition.InputSourceVar, From: "message"},
				},
				ParamTemplates: map[string]definition.ParamTemplate{
					"message": {Segments: []definition.ParamTemplateSegment{{Type: "text", Value: "Hello"}}},
				},
			}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot use both a binding and a template") {
		t.Fatalf("expected parameter binding/template conflict, got %v", err)
	}
}

func TestCompilerCompile_DisabledNodePassesSequenceThrough(t *testing.T) {
	compiler := NewCompiler()
	plan, err := compiler.Compile(&definition.WorkflowVersion{
		ID:         "v-disabled-middle",
		WorkflowID: "wf-disabled-middle",
		Version:    1,
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-disabled-middle",
			EntryNodes: []string{"start"},
			Nodes: []definition.Node{
				{ID: "start", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "RemarkUnit"}},
				{ID: "disabled", Disabled: true, Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TimeoutUnit"}},
				{
					ID:       "finish",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
					InputSpec: &definition.InputSpec{
						Mode: definition.InputModeReplace,
						Bindings: []definition.InputBinding{{
							Source:   definition.InputSourceNode,
							From:     "disabled",
							Required: true,
						}},
					},
					UI: map[string]any{shortcutAutoInputUIKey: "disabled"},
				},
			},
			Edges: []definition.Edge{{From: "start", To: "disabled"}, {From: "disabled", To: "finish"}},
		},
	})
	if err != nil {
		t.Fatalf("compile disabled middle node: %v", err)
	}
	if len(plan.TopologicalOrder) != 2 || plan.TopologicalOrder[0] != "start" || plan.TopologicalOrder[1] != "finish" {
		t.Fatalf("unexpected topological order: %#v", plan.TopologicalOrder)
	}
	if len(plan.Adjacency["start"]) != 1 || plan.Adjacency["start"][0] != "finish" {
		t.Fatalf("disabled node did not bridge adjacency: %#v", plan.Adjacency)
	}
	finish := plan.Nodes["finish"]
	if finish.InputSpec == nil || len(finish.InputSpec.Bindings) != 1 || finish.InputSpec.Bindings[0].From != "start" {
		t.Fatalf("automatic input was not rebound: %#v", finish.InputSpec)
	}
}

func TestCompilerCompile_DisabledEntryAdvancesToActiveNode(t *testing.T) {
	compiler := NewCompiler()
	plan, err := compiler.Compile(&definition.WorkflowVersion{
		ID:         "v-disabled-entry",
		WorkflowID: "wf-disabled-entry",
		Version:    1,
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-disabled-entry",
			EntryNodes: []string{"disabled"},
			Nodes: []definition.Node{
				{ID: "disabled", Disabled: true, Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "RemarkUnit"}},
				{
					ID:       "finish",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
					InputSpec: &definition.InputSpec{
						Mode:     definition.InputModeReplace,
						Bindings: []definition.InputBinding{{Source: definition.InputSourceNode, From: "disabled", Required: true}},
					},
					UI: map[string]any{shortcutAutoInputUIKey: "disabled"},
				},
			},
			Edges: []definition.Edge{{From: "disabled", To: "finish"}},
		},
	})
	if err != nil {
		t.Fatalf("compile disabled entry: %v", err)
	}
	if len(plan.EntryNodes) != 1 || plan.EntryNodes[0] != "finish" {
		t.Fatalf("unexpected resolved entry nodes: %#v", plan.EntryNodes)
	}
	if plan.Nodes["finish"].InputSpec != nil {
		t.Fatalf("entry automatic input should use workflow input: %#v", plan.Nodes["finish"].InputSpec)
	}
}

func TestCompilerCompile_DisabledEntryWithoutSuccessorDoesNotRunUnrelatedNode(t *testing.T) {
	compiler := NewCompiler()
	_, err := compiler.Compile(&definition.WorkflowVersion{
		ID:         "v-disabled-entry-dead-end",
		WorkflowID: "wf-disabled-entry-dead-end",
		Version:    1,
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-disabled-entry-dead-end",
			EntryNodes: []string{"disabled"},
			Nodes: []definition.Node{
				{ID: "disabled", Disabled: true, Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "RemarkUnit"}},
				{ID: "unrelated", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"}},
			},
		},
	})
	if err == nil || err.Error() != "no entry node" {
		t.Fatalf("expected no entry node, got %v", err)
	}
}

func TestCompilerCompile_RejectsExplicitBindingToDisabledNode(t *testing.T) {
	compiler := NewCompiler()
	_, err := compiler.Compile(&definition.WorkflowVersion{
		ID:         "v-disabled-binding",
		WorkflowID: "wf-disabled-binding",
		Version:    1,
		Definition: &definition.WorkflowDefinition{
			ID: "wf-disabled-binding",
			Nodes: []definition.Node{
				{ID: "start", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "RemarkUnit"}},
				{ID: "disabled", Disabled: true, Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "ScriptUnit"}},
				{
					ID:       "finish",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TimeoutUnit"},
					ParamBindings: map[string]definition.InputBinding{
						"duration_ms": {Source: definition.InputSourceNode, From: "disabled", Required: true},
					},
				},
			},
			Edges: []definition.Edge{{From: "start", To: "disabled"}, {From: "disabled", To: "finish"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "references disabled node disabled") {
		t.Fatalf("expected disabled binding error, got %v", err)
	}
}

func TestCompilerCompile_DisabledBranchActionPreservesCondition(t *testing.T) {
	compiler := NewCompiler()
	plan, err := compiler.Compile(&definition.WorkflowVersion{
		ID:         "v-disabled-branch",
		WorkflowID: "wf-disabled-branch",
		Version:    1,
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-disabled-branch",
			EntryNodes: []string{"condition"},
			Nodes: []definition.Node{
				{ID: "condition", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogicUnit"}, Branch: &definition.BranchPolicy{Mode: definition.BranchFirst}},
				{ID: "disabled", Disabled: true, Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TimeoutUnit"}},
				{ID: "end", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "BranchJoinUnit"}},
			},
			Edges: []definition.Edge{
				{From: "condition", To: "disabled", Condition: "Output.ok == true", Label: "Then", Priority: 0},
				{From: "disabled", To: "end"},
				{From: "condition", To: "end", Condition: "!(Output.ok == true)", Label: "Otherwise", Priority: 1},
			},
		},
	})
	if err != nil {
		t.Fatalf("compile disabled branch action: %v", err)
	}
	branch := plan.Branches["condition"]
	if len(branch.Edges) != 2 {
		t.Fatalf("unexpected branch edges: %#v", branch.Edges)
	}
	conditions := map[string]bool{}
	for _, edge := range branch.Edges {
		if edge.To != "end" {
			t.Fatalf("disabled branch did not bridge to end: %#v", branch.Edges)
		}
		conditions[edge.Condition] = true
	}
	if !conditions["Output.ok == true"] || !conditions["!(Output.ok == true)"] {
		t.Fatalf("branch conditions were not preserved: %#v", branch.Edges)
	}
}

func TestCompilerCompile_PlanIdentityIncludesDefinitionContent(t *testing.T) {
	compiler := NewCompiler()
	compile := func(text string) *ExecutionPlan {
		t.Helper()
		plan, err := compiler.Compile(&definition.WorkflowVersion{
			ID:         "wf-plan-identity:latest",
			WorkflowID: "wf-plan-identity",
			Version:    1,
			Definition: &definition.WorkflowDefinition{
				ID:         "wf-plan-identity",
				EntryNodes: []string{"text"},
				Nodes: []definition.Node{{
					ID:       "text",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
					Params:   map[string]any{"text": text},
				}},
			},
		})
		if err != nil {
			t.Fatalf("compile definition: %v", err)
		}
		return plan
	}

	first := compile("first")
	same := compile("first")
	changed := compile("changed")
	if first.PlanID != same.PlanID {
		t.Fatalf("identical definitions produced different plan ids: %q != %q", first.PlanID, same.PlanID)
	}
	if first.PlanID == changed.PlanID {
		t.Fatalf("different definitions produced the same plan id: %q", first.PlanID)
	}
}
