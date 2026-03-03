package workflow_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow"
	"github.com/ninenhan/go-workflow/store"
)

type resumeValueUnit struct {
	workflow.Unit
	name string
}

func (u *resumeValueUnit) GetUnitName() string { return u.name }
func (u *resumeValueUnit) GetUnitMeta() *workflow.Unit { return &u.Unit }
func (u *resumeValueUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	return &workflow.ExecutionResult{
		Data: map[string]any{"value": "ok"},
	}, nil
}

type resumeCheckUnit struct {
	workflow.Unit
	name string
	flag *atomic.Bool
}

func (u *resumeCheckUnit) GetUnitName() string { return u.name }
func (u *resumeCheckUnit) GetUnitMeta() *workflow.Unit { return &u.Unit }
func (u *resumeCheckUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	if u.flag != nil && self != nil && self.Input != nil {
		if v, ok := self.Input.Data.(string); ok && v == "ok" {
			u.flag.Store(true)
		}
	}
	return &workflow.ExecutionResult{Data: self.Input.Data}, nil
}

func TestWorkflowResume(t *testing.T) {
	valueName := "ResumeValueUnit_" + t.Name()
	checkName := "ResumeCheckUnit_" + t.Name()
	var hit atomic.Bool
	workflow.RegisterUnitFactory(valueName, func() workflow.ExecutableUnit {
		u := &resumeValueUnit{name: valueName}
		u.UnitName = u.GetUnitName()
		return u
	})
	workflow.RegisterUnitFactory(checkName, func() workflow.ExecutableUnit {
		u := &resumeCheckUnit{name: checkName, flag: &hit}
		u.UnitName = u.GetUnitName()
		return u
	})

	def := &workflow.WorkflowDefinition{
		ID:    "wf-resume",
		Start: []string{"a"},
		Nodes: map[string]*workflow.NodeSpec{
			"a": {Unit: valueName, ExportFields: []string{"value"}},
			"b": {
				Unit:  checkName,
				Input: &workflow.Input{Data: "{{a.value}}", Slottable: true},
			},
		},
		Edges: []workflow.EdgeSpec{
			{From: "a", To: "b"},
		},
	}

	runID := "run-resume"
	saved := &workflow.ExecutionState{
		WorkflowID: def.ID,
		RunID:      runID,
		Status:     workflow.RunFailed,
		StartedAt:  time.Now().Add(-time.Minute),
		UpdatedAt:  time.Now().Add(-time.Minute),
		Nodes: map[string]*workflow.NodeState{
			"a": {
				ID:     "a",
				Status: workflow.NodeSucceeded,
				Result: &workflow.ExecutionResult{Data: map[string]any{"value": "ok"}},
			},
			"b": {
				ID:     "b",
				Status: workflow.NodePending,
			},
		},
	}

	mem := store.NewMemoryStateStore()
	if err := mem.Save(context.Background(), saved); err != nil {
		t.Fatalf("save state failed: %v", err)
	}

	engine := workflow.NewEngine()
	engine.Store = mem
	state, err := engine.Resume(context.Background(), def, runID, &workflow.RunOptions{Store: mem})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if state.Nodes["b"].Status != workflow.NodeSucceeded {
		t.Fatalf("unexpected node status: %s", state.Nodes["b"].Status)
	}
	if !hit.Load() {
		t.Fatalf("resume did not execute node b with expected input")
	}
}

