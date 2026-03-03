package workflow_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	workflow "github.com/ninenhan/go-workflow"
	"github.com/ninenhan/go-workflow/store"
)

type valueUnit struct {
	workflow.Unit
	name string
}

func (u *valueUnit) GetUnitName() string { return u.name }
func (u *valueUnit) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}

func (u *valueUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	val := ""
	if self != nil && self.Params != nil {
		if v, ok := self.Params["value"].(string); ok {
			val = v
		}
	}
	return &workflow.ExecutionResult{
		NodeName: u.UnitName,
		Data:     map[string]any{"value": val},
	}, nil
}

type passUnit struct {
	workflow.Unit
	name string
}

func (u *passUnit) GetUnitName() string { return u.name }
func (u *passUnit) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}

func (u *passUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, fmt.Errorf("missing input")
	}
	return &workflow.ExecutionResult{
		NodeName: u.UnitName,
		Data:     self.Input.Data,
	}, nil
}

func registerTestUnits(t *testing.T) (string, string) {
	t.Helper()
	suffix := strings.ReplaceAll(t.Name(), "/", "_")
	valueName := "ValueUnit_" + suffix
	passName := "PassUnit_" + suffix
	workflow.RegisterUnitFactory(valueName, func() workflow.ExecutableUnit {
		u := &valueUnit{name: valueName}
		u.UnitName = u.GetUnitName()
		return u
	})
	workflow.RegisterUnitFactory(passName, func() workflow.ExecutableUnit {
		u := &passUnit{name: passName}
		u.UnitName = u.GetUnitName()
		return u
	})
	return valueName, passName
}

func TestWorkflowRunAndResult(t *testing.T) {
	valueName, passName := registerTestUnits(t)

	def := &workflow.WorkflowDefinition{
		ID:    "wf-test",
		Start: []string{"a"},
		Nodes: map[string]*workflow.NodeSpec{
			"a": {
				Name:         "A",
				Unit:         valueName,
				Params:       map[string]any{"value": "world"},
				ExportFields: []string{"value"},
			},
			"b": {
				Name:  "B",
				Unit:  passName,
				Input: &workflow.Input{Data: "hello {{a.value}}", Slottable: true},
			},
		},
		Edges: []workflow.EdgeSpec{{From: "a", To: "b"}},
	}

	engine := workflow.NewEngine()
	state, err := engine.Run(context.Background(), def, &workflow.RunOptions{})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if state.Status != workflow.RunSucceeded {
		t.Fatalf("unexpected status: %s", state.Status)
	}
	got := state.Nodes["b"].Result.Data
	if got != "hello world" {
		t.Fatalf("unexpected result: %v", got)
	}
}

func TestWorkflowLoadAndRun(t *testing.T) {
	valueName, passName := registerTestUnits(t)
	jsonDef := fmt.Sprintf(`{
	  "id": "wf-json",
	  "start": ["a"],
	  "nodes": {
	    "a": {
	      "unit": "%s",
	      "params": { "value": "json" },
	      "export_fields": ["value"]
	    },
	    "b": {
	      "unit": "%s",
	      "input": { "data": "hi {{a.value}}", "slottable": true }
	    }
	  },
	  "edges": {
	    "a": ["b"]
	  }
	}`, valueName, passName)

	def, err := workflow.ParseWorkflowJSON([]byte(jsonDef))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	mem := store.NewMemoryStateStore()
	engine := workflow.NewEngine()
	runID := "run-json"
	state, err := engine.Run(context.Background(), def, &workflow.RunOptions{
		RunID: runID,
		Store: mem,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if state.Nodes["b"].Result.Data != "hi json" {
		t.Fatalf("unexpected result: %v", state.Nodes["b"].Result.Data)
	}

	loaded, err := mem.Load(context.Background(), def.ID, runID)
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if loaded.Status != workflow.RunSucceeded {
		t.Fatalf("unexpected loaded status: %s", loaded.Status)
	}
}
