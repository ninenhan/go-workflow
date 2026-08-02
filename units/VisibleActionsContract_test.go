package units

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	coreexecutor "github.com/ninenhan/go-workflow/core/executor"
	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

type visibleActionContract struct {
	name     string
	input    any
	params   map[string]any
	context  map[string]any
	validate func(*testing.T, coreexecutor.ExecuteResult)
}

func exactOutput(want any) func(*testing.T, coreexecutor.ExecuteResult) {
	return func(t *testing.T, result coreexecutor.ExecuteResult) {
		t.Helper()
		if !reflect.DeepEqual(result.Output, want) {
			t.Fatalf("output = %#v, want %#v", result.Output, want)
		}
	}
}

func TestUserVisibleRuntimeUnitsExecuteThroughWorkerContract(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "visible-action-contract-key")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/http":
			_ = json.NewEncoder(response).Encode(map[string]any{"ok": true})
		case "/llm":
			if request.Header.Get("Authorization") != "Bearer visible-action-contract-key" {
				t.Errorf("LLM authorization header = %q", request.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"choices": []any{
					map[string]any{
						"message": map[string]any{"content": "model output"},
					},
				},
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	contracts := []visibleActionContract{
		{
			name:     "AdjustDateUnit",
			input:    "2026-01-01T09:30:00Z",
			params:   map[string]any{"operation": "add", "amount": 1, "unit": "day"},
			validate: exactOutput("2026-01-02T09:30:00Z"),
		},
		{
			name:     "AskForInputUnit",
			input:    "Ada",
			params:   map[string]any{"input_type": "text"},
			validate: exactOutput("Ada"),
		},
		{
			name:     "CalculateUnit",
			params:   map[string]any{"left": 6, "operation": "multiply", "right": 7},
			validate: exactOutput(float64(42)),
		},
		{
			name:     "ChangeCaseUnit",
			input:    "hello workflow",
			params:   map[string]any{"case": "title"},
			validate: exactOutput("Hello Workflow"),
		},
		{
			name:     "ChooseFromMenuUnit",
			params:   map[string]any{"items": []any{"alpha", "beta"}, "choice": "beta"},
			validate: exactOutput("beta"),
		},
		{
			name:     "CombineTextUnit",
			input:    []any{"alpha", "beta"},
			params:   map[string]any{"separator_mode": "spaces"},
			validate: exactOutput("alpha beta"),
		},
		{
			name:     "CountUnit",
			input:    []any{"alpha", "beta"},
			params:   map[string]any{"count": "items"},
			validate: exactOutput(2),
		},
		{
			name: "DateUnit",
			params: map[string]any{
				"mode": "specified", "date": "2026-07-28", "time": "09:30", "timezone": "UTC",
			},
			validate: exactOutput("2026-07-28T09:30:00Z"),
		},
		{
			name: "DictionaryUnit",
			params: map[string]any{
				"entries": []any{map[string]any{"key": "name", "type": "text", "value": "Ada"}},
			},
			validate: exactOutput(map[string]any{"name": "Ada"}),
		},
		{
			name:     "FormatDateUnit",
			input:    "2026-07-28T09:30:00Z",
			params:   map[string]any{"format": "date"},
			validate: exactOutput("2026-07-28"),
		},
		{
			name:     "GetDictionaryValueUnit",
			input:    map[string]any{"name": "Ada"},
			params:   map[string]any{"key": "name"},
			validate: exactOutput("Ada"),
		},
		{
			name:     "GetListItemUnit",
			input:    []any{"alpha", "beta"},
			params:   map[string]any{"operation": "last"},
			validate: exactOutput("beta"),
		},
		{
			name:   "HttpUnit",
			params: map[string]any{"url": server.URL + "/http", "method": "GET", "timeout_ms": 1000},
			validate: func(t *testing.T, result coreexecutor.ExecuteResult) {
				t.Helper()
				output, ok := result.Output.(map[string]any)
				if !ok || output["status"] != http.StatusOK {
					t.Fatalf("HTTP output = %#v", result.Output)
				}
				body, ok := output["body"].(map[string]any)
				if !ok || body["ok"] != true {
					t.Fatalf("HTTP body = %#v", output["body"])
				}
			},
		},
		{
			name:     "IfUnit",
			input:    map[string]any{"branch": "then"},
			validate: exactOutput(map[string]any{"branch": "then"}),
		},
		{
			name:  "LLMUnit",
			input: "Generate a result",
			params: map[string]any{
				"base_url": server.URL + "/llm", "model": "contract-model", "timeout_ms": 1000,
			},
			validate: func(t *testing.T, result coreexecutor.ExecuteResult) {
				t.Helper()
				output, ok := result.Output.(map[string]any)
				if !ok || output["text"] != "model output" {
					t.Fatalf("LLM output = %#v", result.Output)
				}
			},
		},
		{
			name:     "ListUnit",
			params:   map[string]any{"items": []any{"alpha", "beta"}},
			validate: exactOutput([]any{"alpha", "beta"}),
		},
		{
			name:     "LogUnit",
			input:    "preview",
			validate: exactOutput("preview"),
		},
		{
			name:     "ReadableUnit",
			input:    map[string]any{"name": "Ada"},
			params:   map[string]any{"format": "json"},
			validate: exactOutput("{\n  \"name\": \"Ada\"\n}"),
		},
		{
			name:     "RemarkUnit",
			input:    "note",
			validate: exactOutput("note"),
		},
		{
			name:     "ReplaceTextUnit",
			input:    "alpha alpha",
			params:   map[string]any{"find": "alpha", "replacement": "beta"},
			validate: exactOutput("beta beta"),
		},
		{
			name:     "ScriptUnit",
			input:    4,
			params:   map[string]any{"language": "javascript", "script": "input * 2"},
			validate: exactOutput(map[string]any{"$$": int64(8)}),
		},
		{
			name:    "SetEnvUnit",
			input:   map[string]any{"profile": map[string]any{"role": "admin"}},
			params:  map[string]any{"variable_name": "profile", "mode": "merge", "value_path": "profile"},
			context: map[string]any{"profile": map[string]any{"name": "Ada"}},
			validate: func(t *testing.T, result coreexecutor.ExecuteResult) {
				t.Helper()
				wantInput := map[string]any{"profile": map[string]any{"role": "admin"}}
				if !reflect.DeepEqual(result.Output, wantInput) {
					t.Fatalf("SetEnv output = %#v", result.Output)
				}
				wantVariables := map[string]any{
					"profile": map[string]any{"name": "Ada", "role": "admin"},
				}
				if !reflect.DeepEqual(result.Variables, wantVariables) {
					t.Fatalf("SetEnv variables = %#v", result.Variables)
				}
			},
		},
		{
			name:     "SplitTextUnit",
			input:    "alpha beta",
			params:   map[string]any{"separator_mode": "spaces"},
			validate: exactOutput([]any{"alpha", "beta"}),
		},
		{
			name:     "TerminalUnit",
			input:    map[string]any{"name": "Ada"},
			params:   map[string]any{"response_mode": "text", "text_template": "Hello {{.name}}"},
			validate: exactOutput("Hello Ada"),
		},
		{
			name:     "TextUnit",
			params:   map[string]any{"text": "hello"},
			validate: exactOutput("hello"),
		},
		{
			name:     "TimeoutUnit",
			input:    "waited",
			params:   map[string]any{"duration_ms": 1},
			validate: exactOutput("waited"),
		},
	}

	covered := make(map[string]struct{}, len(contracts))
	for _, contract := range contracts {
		if _, exists := covered[contract.name]; exists {
			t.Fatalf("duplicate visible action contract for %s", contract.name)
		}
		covered[contract.name] = struct{}{}
	}
	for _, name := range userVisibleRuntimeUnitNames {
		if _, exists := covered[name]; !exists {
			t.Errorf("missing visible action contract for %s", name)
		}
	}
	if len(covered) != len(userVisibleRuntimeUnitNames) {
		t.Fatalf("visible action contracts = %d, runtime actions = %d", len(covered), len(userVisibleRuntimeUnitNames))
	}

	executor := workerunit.NewExecutor(nil)
	for _, contract := range contracts {
		t.Run(contract.name, func(t *testing.T) {
			result, err := executor.Execute(context.Background(), coreexecutor.ExecuteTask{
				RunID:        "visible-action-contract",
				NodeID:       contract.name,
				ExecutorType: string(coreexecutor.TypeUnit),
				ExecutorRef:  contract.name,
				Input:        contract.input,
				Params:       contract.params,
				Context:      contract.context,
			})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if result.Status != coreexecutor.StatusSucceeded {
				t.Fatalf("status = %q", result.Status)
			}
			if result.Metadata["node_name"] != contract.name {
				t.Fatalf("node name = %#v", result.Metadata["node_name"])
			}
			contract.validate(t, result)
		})
	}
}
