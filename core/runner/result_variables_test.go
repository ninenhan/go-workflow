package runner

import (
	"testing"

	"github.com/ninenhan/go-workflow/core/executor"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
)

func TestApplyResultVariables(t *testing.T) {
	run := wfruntime.NewWorkflowRun("run-1", "workflow-1", "version-1", "plan-1")
	run.Context.Variables["keep"] = "old"
	run.Context.Variables["remove"] = true

	applyResultVariables(run, executor.ExecuteResult{
		Variables:       map[string]any{"keep": "new", "created": 42},
		DeleteVariables: []string{"remove"},
	})

	if run.Context.Variables["keep"] != "new" || run.Context.Variables["created"] != 42 {
		t.Fatalf("variable updates were not applied: %#v", run.Context.Variables)
	}
	if _, exists := run.Context.Variables["remove"]; exists {
		t.Fatalf("deleted variable remains: %#v", run.Context.Variables)
	}
}
