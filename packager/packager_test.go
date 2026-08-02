package packager

import (
	"strings"
	"testing"

	"github.com/ninenhan/go-workflow/core/definition"
)

func TestValidateStandalone_AllowsBuiltinUnits(t *testing.T) {
	err := ValidateStandalone(&definition.WorkflowDefinition{
		ID: "wf-demo",
		Nodes: []definition.Node{
			{
				ID:       "n1",
				Executor: definition.ExecutorSpec{Type: "unit", Ref: "LogUnit"},
			},
			{
				ID:       "n2",
				Executor: definition.ExecutorSpec{Type: "http"},
			},
		},
	})
	if err != nil {
		t.Fatalf("ValidateStandalone() error = %v", err)
	}
}

func TestValidateStandalone_RejectsCustomUnitsAndLocalGo(t *testing.T) {
	err := ValidateStandalone(&definition.WorkflowDefinition{
		ID: "wf-demo",
		Nodes: []definition.Node{
			{
				ID:       "custom",
				Executor: definition.ExecutorSpec{Type: "unit", Ref: "GreetingUnit"},
			},
			{
				ID:       "local",
				Executor: definition.ExecutorSpec{Type: "local_go"},
			},
		},
	})
	if err == nil {
		t.Fatal("ValidateStandalone() expected error")
	}
	message := err.Error()
	if !strings.Contains(message, "GreetingUnit") {
		t.Fatalf("expected custom unit error, got %q", message)
	}
	if !strings.Contains(message, "local_go") {
		t.Fatalf("expected local_go error, got %q", message)
	}
}

func TestRenderMain(t *testing.T) {
	source, err := RenderMain(&definition.WorkflowDefinition{
		ID:   "wf-demo",
		Name: "demo",
		Nodes: []definition.Node{
			{
				ID:       "n1",
				Executor: definition.ExecutorSpec{Type: "unit", Ref: "LogUnit"},
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderMain() error = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "standalone.RunCLI") {
		t.Fatalf("generated source missing RunCLI call: %s", text)
	}
	if !strings.Contains(text, "workflowDefinitionJSON") {
		t.Fatalf("generated source missing definition constant: %s", text)
	}
}
