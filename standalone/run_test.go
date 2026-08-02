package standalone

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ninenhan/go-workflow/core/definition"
)

func TestRunDefinition_InputSources(t *testing.T) {
	def := loadDefinitionFixture(t, "input-sources.workflow.json")

	run, err := RunDefinition(context.Background(), RunnerConfig{
		Definition: def,
	}, RunInput{
		Vars: map[string]any{
			"tenant_id": "t-001",
		},
		Request: map[string]any{
			"body": map[string]any{
				"message": "hello-standalone",
			},
		},
	})
	if err != nil {
		t.Fatalf("run workflow: %v", err)
	}
	if got := string(run.Status); got != "success" {
		t.Fatalf("unexpected run status: %s", got)
	}

	result, ok := run.Context.NodeResults["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary result type = %T", run.Context.NodeResults["summary"])
	}
	if result["tenant_id"] != "t-001" {
		t.Fatalf("tenant_id = %#v", result["tenant_id"])
	}
	if result["message"] != "hello-standalone" {
		t.Fatalf("message = %#v", result["message"])
	}
	if result["workflow_id"] != "wf-input-sources-demo" {
		t.Fatalf("workflow_id = %#v", result["workflow_id"])
	}
}

func loadDefinitionFixture(t *testing.T, name string) *definition.WorkflowDefinition {
	t.Helper()
	path := filepath.Join("..", "examples", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	out, err := parseDefinitionJSON(string(raw))
	if err != nil {
		t.Fatalf("parse workflow definition: %v", err)
	}
	return out
}
