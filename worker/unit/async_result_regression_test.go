package unit

import (
	"context"
	"reflect"
	"testing"

	"github.com/ninenhan/go-workflow/core/executor"
)

type asyncResultRegressionUnit struct {
	Unit
	result *ExecutionResult
}

func (u *asyncResultRegressionUnit) GetUnitMeta() *Unit { return &u.Unit }

func (u *asyncResultRegressionUnit) Execute(context.Context, ContextMap, *Node) (*ExecutionResult, error) {
	return u.result, nil
}

func TestAsyncUnitResultRegression(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status executor.Status
		id     string
		bad    bool
	}{
		{name: "legacy_success"},
		{name: "success", status: executor.StatusSucceeded},
		{name: "failed", status: executor.StatusFailed},
		{name: "retryable", status: executor.StatusRetryable},
		{name: "accepted", status: executor.StatusAccepted, id: "external-1"},
		{name: "running", status: executor.StatusRunning, id: "external-2"},
		{name: "missing_id", status: executor.StatusAccepted, bad: true},
		{name: "blank_id", status: executor.StatusRunning, id: " \t", bad: true},
		{name: "unknown_status", status: executor.Status("unknown"), bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := &ExecutionResult{
				Status: tc.status, ExternalTaskID: tc.id, Data: "output",
				Variables:       map[string]any{"artistIds": []any{}},
				DeleteVariables: []string{"obsolete"}, Error: "diagnostic",
			}
			reg := NewRegistry()
			if err := reg.Register("async-result", func() Executable {
				return &asyncResultRegressionUnit{result: input}
			}); err != nil {
				t.Fatal(err)
			}
			result, err := NewExecutor(reg).Execute(context.Background(), executor.ExecuteTask{ExecutorRef: "async-result"})
			if tc.bad {
				if err == nil {
					t.Fatalf("invalid result accepted: %+v", result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := tc.status
			if wantStatus == "" {
				wantStatus = executor.StatusSucceeded
			}
			wantCallback := wantStatus == executor.StatusAccepted || wantStatus == executor.StatusRunning
			if result.AwaitCallback != wantCallback {
				t.Fatalf("callback delivery flag=%v want=%v", result.AwaitCallback, wantCallback)
			}
			if result.Status != wantStatus || result.ExternalTaskID != tc.id || result.Output != input.Data ||
				!reflect.DeepEqual(result.Variables, input.Variables) || !reflect.DeepEqual(result.DeleteVariables, input.DeleteVariables) {
				t.Fatalf("result fields were not preserved: %+v", result)
			}
		})
	}
}
