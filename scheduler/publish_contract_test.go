package scheduler

import (
	"testing"

	"github.com/ninenhan/go-workflow/core/definition"
)

func publishedTestText(value string) map[string]any {
	return map[string]any{
		"zh-CN": value, "zh-TW": value, "en": value,
		"ja": value, "es": value, "bo": value,
	}
}

func TestPublishedAPIInputContractValidatesAndNormalizesQuery(t *testing.T) {
	def := &definition.WorkflowDefinition{Metadata: map[string]any{
		"run_form": map[string]any{"fields": []any{
			map[string]any{
				"key": "plan", "kind": "select", "label": publishedTestText("Plan"), "default": "basic",
				"options": []any{
					map[string]any{"value": "basic", "label": publishedTestText("Basic")},
					map[string]any{"value": "pro", "label": publishedTestText("Pro")},
				},
			},
			map[string]any{"key": "count", "kind": "number", "label": publishedTestText("Count"), "required": true},
			map[string]any{"key": "enabled", "kind": "switch", "label": publishedTestText("Enabled"), "default": false},
		}},
	}}
	variables := map[string]any{"count": "3", "enabled": "true"}
	if err := applyPublishedInputContract(def, variables, "query"); err != nil {
		t.Fatalf("apply input contract: %v", err)
	}
	if variables["plan"] != "basic" || variables["count"] != float64(3) || variables["enabled"] != true {
		t.Fatalf("normalized variables = %#v", variables)
	}

	invalid := map[string]any{"plan": "enterprise", "count": "1"}
	if err := applyPublishedInputContract(def, invalid, "query"); err == nil {
		t.Fatal("expected invalid select option to fail")
	}
	missing := map[string]any{}
	if err := applyPublishedInputContract(def, missing, "query"); err == nil {
		t.Fatal("expected required count to fail")
	}
}

func TestBuildPublishedAPIContractIncludesLocalizedInputsAndExample(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "wf-contract", Name: "Contract", Description: "Service contract",
		PublishConfig: &definition.PublishConfig{
			Enabled: true, Route: "/api/contract", Method: "POST", InputMode: "body", ResponseMode: "run", TimeoutMS: 10000,
		},
		Metadata: map[string]any{"run_form": map[string]any{"fields": []any{
			map[string]any{"key": "message", "kind": "text", "label": publishedTestText("Message"), "required": true},
		}}},
	}
	contract, err := buildPublishedAPIContract(&definition.WorkflowVersion{
		ID: "wf-contract:v2", WorkflowID: "wf-contract", Version: 2, Status: definition.VersionPublished, Definition: def,
	})
	if err != nil {
		t.Fatalf("build contract: %v", err)
	}
	if contract.Route != "/api/contract" || contract.Method != "POST" || contract.Version != 2 || contract.TimeoutMS != 10000 {
		t.Fatalf("contract identity = %+v", contract)
	}
	if len(contract.Inputs) != 1 || contract.Inputs[0].Label["bo"] != "Message" || contract.Example["message"] != "string" {
		t.Fatalf("contract inputs = %+v example=%#v", contract.Inputs, contract.Example)
	}
}
