package unit

import (
	"context"
	"testing"

	"github.com/ninenhan/go-workflow/core/executor"
)

type configuredUnit struct {
	Unit
	Message string `json:"message"`
}

func (u *configuredUnit) GetUnitName() string { return "ConfiguredUnit" }

func (u *configuredUnit) GetUnitMeta() *Unit { return &u.Unit }

func (u *configuredUnit) Execute(ctx context.Context, state ContextMap, self *Node) (*ExecutionResult, error) {
	return &ExecutionResult{
		NodeName: u.UnitName,
		Data: map[string]any{
			"message": u.Message,
			"input":   self.Input.Data,
			"prev":    state["prev"].Data,
		},
	}, nil
}

func TestExecutorExecute_HydratesUnitAndContext(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterUnitFactory("ConfiguredUnit", func() ExecutableUnit {
		unit := &configuredUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})

	execImpl := NewExecutor(reg)
	result, err := execImpl.Execute(context.Background(), executor.ExecuteTask{
		RunID:        "run-1",
		NodeID:       "node-1",
		ExecutorType: string(executor.TypeUnit),
		ExecutorRef:  "ConfiguredUnit",
		Input:        "hello",
		Params: map[string]any{
			"message": "from-params",
		},
		Context: map[string]any{
			"prev": "from-context",
		},
	})
	if err != nil {
		t.Fatalf("execute unit: %v", err)
	}
	if result.Status != executor.StatusSucceeded {
		t.Fatalf("unexpected status: %s", result.Status)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("unexpected output type: %T", result.Output)
	}
	if got := output["message"]; got != "from-params" {
		t.Fatalf("unexpected message: %#v", got)
	}
	if got := output["input"]; got != "hello" {
		t.Fatalf("unexpected input: %#v", got)
	}
	if got := output["prev"]; got != "from-context" {
		t.Fatalf("unexpected context value: %#v", got)
	}
}
