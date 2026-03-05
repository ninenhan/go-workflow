package workflow_test

import (
	"context"
	"testing"

	workflow "github.com/ninenhan/go-workflow"
	_ "github.com/ninenhan/go-workflow/plugins/uppercase"
)

func TestPluginAutoRegisterByBlankImport(t *testing.T) {
	def := &workflow.WorkflowDefinition{
		ID:    "wf-plugin-demo",
		Start: []string{"upper"},
		Nodes: map[string]*workflow.NodeSpec{
			"upper": {
				Name: "Upper",
				Unit: "UppercaseUnit",
				Input: &workflow.Input{
					Data: "hello plugin",
				},
			},
		},
	}

	engine := workflow.NewEngine()
	state, err := engine.Run(context.Background(), def, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	node := state.Nodes["upper"]
	if node == nil || node.Result == nil {
		t.Fatalf("missing node result: %+v", node)
	}
	if got := node.Result.Data; got != "HELLO PLUGIN" {
		t.Fatalf("unexpected output: %v", got)
	}
}
