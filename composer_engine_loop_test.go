package workflow_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	_ "github.com/ninenhan/go-workflow/units"

	"github.com/ninenhan/go-workflow"
)

type counterUnit struct {
	workflow.Unit
	name string
}

func (u *counterUnit) GetUnitName() string { return u.name }
func (u *counterUnit) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}
func (u *counterUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	if self == nil || self.Params == nil {
		return nil, errors.New("missing params")
	}
	counter, _ := self.Params["counter"].(*int32)
	if counter == nil {
		return nil, errors.New("counter missing")
	}
	n := atomic.AddInt32(counter, 1)
	return &workflow.ExecutionResult{
		Data: map[string]any{"count": int(n)},
	}, nil
}

type markUnit struct {
	workflow.Unit
	name string
}

func (u *markUnit) GetUnitName() string { return u.name }
func (u *markUnit) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}
func (u *markUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	if self == nil || self.Params == nil {
		return nil, errors.New("missing params")
	}
	counter, _ := self.Params["counter"].(*int32)
	if counter == nil {
		return nil, errors.New("counter missing")
	}
	n := atomic.AddInt32(counter, 1)
	return &workflow.ExecutionResult{
		Data: map[string]any{"marked": int(n)},
	}, nil
}

func TestWhileBreak(t *testing.T) {
	counterName := "CounterUnit_" + t.Name()
	workflow.RegisterUnitFactory(counterName, func() workflow.ExecutableUnit {
		u := &counterUnit{name: counterName}
		u.UnitName = u.GetUnitName()
		return u
	})

	var counter int32
	body := &workflow.WorkflowDefinition{
		Start: []string{"inc"},
		Nodes: map[string]*workflow.NodeSpec{
			"inc": {
				Unit:    counterName,
				Params:  map[string]any{"counter": &counter},
				Options: &workflow.NodeOptions{BranchMode: workflow.BranchFirst},
			},
			"break": {Unit: "BreakUnit"},
			"noop":  {Unit: "LogicUnit"},
		},
		Edges: []workflow.EdgeSpec{
			{From: "inc", To: "break", When: "Result.Data[\"count\"] >= 3", Order: 0},
			{From: "inc", To: "noop", Order: 1},
		},
	}

	def := &workflow.WorkflowDefinition{
		ID:    "wf-while-break",
		Start: []string{"loop"},
		Nodes: map[string]*workflow.NodeSpec{
			"loop": {
				Unit: "WhileUnit",
				Params: map[string]any{
					"condition":   "Iter < 10",
					"max":         10,
					"body":        body,
					"result_node": "inc",
				},
			},
		},
	}

	engine := workflow.NewEngine()
	state, err := engine.Run(context.Background(), def, &workflow.RunOptions{})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if state.Status != workflow.RunSucceeded {
		t.Fatalf("unexpected run status: %s", state.Status)
	}
	if got := atomic.LoadInt32(&counter); got != 3 {
		t.Fatalf("unexpected counter: %d", got)
	}
}

func TestWhileContinue(t *testing.T) {
	counterName := "CounterUnit_" + t.Name()
	markName := "MarkUnit_" + t.Name()
	workflow.RegisterUnitFactory(counterName, func() workflow.ExecutableUnit {
		u := &counterUnit{name: counterName}
		u.UnitName = u.GetUnitName()
		return u
	})
	workflow.RegisterUnitFactory(markName, func() workflow.ExecutableUnit {
		u := &markUnit{name: markName}
		u.UnitName = u.GetUnitName()
		return u
	})

	var counter int32
	var marked int32
	body := &workflow.WorkflowDefinition{
		Start: []string{"inc"},
		Nodes: map[string]*workflow.NodeSpec{
			"inc": {
				Unit:    counterName,
				Params:  map[string]any{"counter": &counter},
				Options: &workflow.NodeOptions{BranchMode: workflow.BranchFirst},
			},
			"cont": {Unit: "ContinueUnit"},
			"mark": {
				Unit:   markName,
				Params: map[string]any{"counter": &marked},
			},
		},
		Edges: []workflow.EdgeSpec{
			{From: "inc", To: "cont", When: "Result.Data[\"count\"] < 3", Order: 0},
			{From: "inc", To: "mark", Order: 1},
		},
	}

	def := &workflow.WorkflowDefinition{
		ID:    "wf-while-continue",
		Start: []string{"loop"},
		Nodes: map[string]*workflow.NodeSpec{
			"loop": {
				Unit: "WhileUnit",
				Params: map[string]any{
					"condition": "Iter < 3",
					"max":       5,
					"body":      body,
				},
			},
		},
	}

	engine := workflow.NewEngine()
	state, err := engine.Run(context.Background(), def, &workflow.RunOptions{})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if state.Status != workflow.RunSucceeded {
		t.Fatalf("unexpected run status: %s", state.Status)
	}
	if got := atomic.LoadInt32(&counter); got != 3 {
		t.Fatalf("unexpected counter: %d", got)
	}
	if got := atomic.LoadInt32(&marked); got != 1 {
		t.Fatalf("unexpected marked count: %d", got)
	}
}
