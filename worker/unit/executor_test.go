package unit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
)

type classifiedFailureUnit struct {
	Unit
	Retryable bool `json:"retryable"`
}

func (u *classifiedFailureUnit) GetUnitName() string { return "ClassifiedFailureUnit" }

func (u *classifiedFailureUnit) GetUnitMeta() *Unit { return &u.Unit }

func (u *classifiedFailureUnit) Execute(context.Context, ContextMap, *Node) (*ExecutionResult, error) {
	err := errors.New("classified failure")
	if u.Retryable {
		return nil, executor.RetryableFailure(err, 250*time.Millisecond)
	}
	return nil, executor.PermanentFailure(err)
}

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
		Variables:       map[string]any{"saved": self.Input.Data},
		DeleteVariables: []string{"obsolete"},
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
	if got := result.Variables["saved"]; got != "hello" {
		t.Fatalf("unexpected variable update: %#v", got)
	}
	if len(result.DeleteVariables) != 1 || result.DeleteVariables[0] != "obsolete" {
		t.Fatalf("unexpected deleted variables: %#v", result.DeleteVariables)
	}
}

func TestExecutorExecute_PreservesClassifiedFailureDisposition(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterUnitFactory("ClassifiedFailureUnit", func() ExecutableUnit {
		unit := &classifiedFailureUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
	execImpl := NewExecutor(reg)

	for _, test := range []struct {
		name       string
		retryable  bool
		wantStatus executor.Status
		wantAfter  time.Duration
	}{
		{name: "permanent", wantStatus: executor.StatusFailed},
		{name: "retryable", retryable: true, wantStatus: executor.StatusRetryable, wantAfter: 250 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := execImpl.Execute(context.Background(), executor.ExecuteTask{
				RunID:        "run-classified",
				NodeID:       "node-classified",
				ExecutorType: string(executor.TypeUnit),
				ExecutorRef:  "ClassifiedFailureUnit",
				Params:       map[string]any{"retryable": test.retryable},
			})
			if err != nil {
				t.Fatalf("execute classified failure unit: %v", err)
			}
			if result.Status != test.wantStatus || result.Error != "classified failure" || result.RetryAfter != test.wantAfter {
				t.Fatalf("unexpected classified result: %#v", result)
			}
		})
	}
}
