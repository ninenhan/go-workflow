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

type compositionStep struct {
	unit   string
	input  any
	params map[string]any
}

func compositionOutputAtPath(t *testing.T, output any, path string) any {
	t.Helper()
	if path == "" {
		return output
	}
	record, ok := output.(map[string]any)
	if !ok {
		t.Fatalf("composition output path %q requires an object, got %#v", path, output)
	}
	value, exists := record[path]
	if !exists {
		t.Fatalf("composition output path %q is missing from %#v", path, output)
	}
	return value
}

func executeCompositionStep(
	t *testing.T,
	executor *workerunit.Executor,
	name string,
	step compositionStep,
) any {
	t.Helper()
	result, err := executor.Execute(context.Background(), coreexecutor.ExecuteTask{
		RunID:        "visible-action-composition",
		NodeID:       name,
		ExecutorType: string(coreexecutor.TypeUnit),
		ExecutorRef:  step.unit,
		Input:        step.input,
		Params:       step.params,
	})
	if err != nil {
		t.Fatalf("%s execute: %v", step.unit, err)
	}
	if result.Status != coreexecutor.StatusSucceeded {
		t.Fatalf("%s status = %q", step.unit, result.Status)
	}
	return result.Output
}

func TestVisibleActionsComposeAcrossDeclaredValueKinds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"message": "hello from API",
			"items":   []any{"alpha", "beta"},
		})
	}))
	defer server.Close()

	tests := []struct {
		name       string
		source     compositionStep
		sourcePath string
		target     compositionStep
		want       any
	}{
		{
			name:   "text_to_text",
			source: compositionStep{unit: "TextUnit", params: map[string]any{"text": "hello workflow"}},
			target: compositionStep{unit: "ChangeCaseUnit", params: map[string]any{"case": "upper"}},
			want:   "HELLO WORKFLOW",
		},
		{
			name:   "text_to_list",
			source: compositionStep{unit: "TextUnit", params: map[string]any{"text": "alpha beta"}},
			target: compositionStep{unit: "SplitTextUnit", params: map[string]any{"separator_mode": "spaces"}},
			want:   []any{"alpha", "beta"},
		},
		{
			name:   "list_to_text",
			source: compositionStep{unit: "ListUnit", params: map[string]any{"items": []any{"alpha", "beta"}}},
			target: compositionStep{unit: "CombineTextUnit", params: map[string]any{
				"separator_mode": "custom", "custom_separator": ",",
			}},
			want: "alpha,beta",
		},
		{
			name:   "list_to_item",
			source: compositionStep{unit: "ListUnit", params: map[string]any{"items": []any{"alpha", "beta"}}},
			target: compositionStep{unit: "GetListItemUnit", params: map[string]any{"operation": "last"}},
			want:   "beta",
		},
		{
			name:   "list_to_number",
			source: compositionStep{unit: "ListUnit", params: map[string]any{"items": []any{"alpha", "beta"}}},
			target: compositionStep{unit: "CountUnit", params: map[string]any{"count": "items"}},
			want:   2,
		},
		{
			name: "dictionary_to_value",
			source: compositionStep{unit: "DictionaryUnit", params: map[string]any{
				"entries": []any{map[string]any{"key": "name", "type": "text", "value": "Ada"}},
			}},
			target: compositionStep{unit: "GetDictionaryValueUnit", params: map[string]any{"key": "name"}},
			want:   "Ada",
		},
		{
			name: "dictionary_to_number",
			source: compositionStep{unit: "DictionaryUnit", params: map[string]any{
				"entries": []any{
					map[string]any{"key": "name", "type": "text", "value": "Ada"},
					map[string]any{"key": "active", "type": "boolean", "value": true},
				},
			}},
			target: compositionStep{unit: "CountUnit", params: map[string]any{"count": "items"}},
			want:   2,
		},
		{
			name: "date_to_date",
			source: compositionStep{unit: "DateUnit", params: map[string]any{
				"mode": "specified", "date": "2026-07-28", "time": "09:30", "timezone": "UTC",
			}},
			target: compositionStep{unit: "AdjustDateUnit", params: map[string]any{
				"operation": "add", "amount": 1, "unit": "day",
			}},
			want: "2026-07-29T09:30:00Z",
		},
		{
			name: "date_to_text",
			source: compositionStep{unit: "DateUnit", params: map[string]any{
				"mode": "specified", "date": "2026-07-28", "time": "09:30", "timezone": "UTC",
			}},
			target: compositionStep{unit: "FormatDateUnit", params: map[string]any{"format": "date"}},
			want:   "2026-07-28",
		},
		{
			name: "date_to_readable",
			source: compositionStep{unit: "DateUnit", params: map[string]any{
				"mode": "specified", "date": "2026-07-28", "time": "09:30", "timezone": "UTC",
			}},
			target: compositionStep{unit: "ReadableUnit"},
			want:   "2026-07-28T09:30:00Z",
		},
		{
			name: "boolean_to_readable",
			source: compositionStep{unit: "DictionaryUnit", params: map[string]any{
				"entries": []any{
					map[string]any{"key": "enabled", "type": "boolean", "value": true},
				},
			}},
			sourcePath: "enabled",
			target:     compositionStep{unit: "ReadableUnit"},
			want:       "true",
		},
		{
			name:   "list_to_readable",
			source: compositionStep{unit: "ListUnit", params: map[string]any{"items": []any{"alpha", "beta"}}},
			target: compositionStep{unit: "ReadableUnit", params: map[string]any{"format": "json"}},
			want:   "[\n  \"alpha\",\n  \"beta\"\n]",
		},
		{
			name: "dictionary_to_readable",
			source: compositionStep{unit: "DictionaryUnit", params: map[string]any{
				"entries": []any{
					map[string]any{"key": "name", "type": "text", "value": "Ada"},
				},
			}},
			target: compositionStep{unit: "ReadableUnit", params: map[string]any{"format": "json"}},
			want:   "{\n  \"name\": \"Ada\"\n}",
		},
		{
			name: "response_to_text",
			source: compositionStep{unit: "HttpUnit", params: map[string]any{
				"url": server.URL, "method": "GET", "timeout_ms": 1000,
			}},
			target: compositionStep{unit: "ReadableUnit", params: map[string]any{"format": "json"}},
			want:   "{\n  \"body\": {\n    \"items\": [\n      \"alpha\",\n      \"beta\"\n    ],\n    \"message\": \"hello from API\"\n  },\n  \"headers\": {\n    \"Content-Length\": [\n      \"60\"\n    ],\n    \"Content-Type\": [\n      \"application/json\"\n    ],\n    \"Date\": [\n      \"Tue, 28 Jul 2026 00:00:00 GMT\"\n    ]\n  },\n  \"status\": 200\n}",
		},
		{
			name:   "number_to_text",
			source: compositionStep{unit: "CountUnit", input: []any{"alpha", "beta"}, params: map[string]any{"count": "items"}},
			target: compositionStep{unit: "ReadableUnit", params: map[string]any{"format": "text"}},
			want:   "2",
		},
		{
			name:   "any_to_any",
			source: compositionStep{unit: "RemarkUnit", input: map[string]any{"ok": true}},
			target: compositionStep{unit: "LogUnit"},
			want:   map[string]any{"ok": true},
		},
	}

	executor := workerunit.NewExecutor(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sourceOutput := executeCompositionStep(t, executor, test.name+"-source", test.source)
			target := test.target
			target.input = compositionOutputAtPath(t, sourceOutput, test.sourcePath)
			output := executeCompositionStep(t, executor, test.name+"-target", target)
			if test.name == "response_to_text" {
				rendered, ok := output.(string)
				if !ok {
					t.Fatalf("readable response = %#v, want text", output)
				}
				var decoded map[string]any
				if err := json.Unmarshal([]byte(rendered), &decoded); err != nil {
					t.Fatalf("decode readable response: %v", err)
				}
				if decoded["status"] != float64(http.StatusOK) {
					t.Fatalf("response status = %#v", decoded["status"])
				}
				body, ok := decoded["body"].(map[string]any)
				if !ok || body["message"] != "hello from API" {
					t.Fatalf("response body = %#v", decoded["body"])
				}
				return
			}
			if !reflect.DeepEqual(output, test.want) {
				t.Fatalf("output = %#v, want %#v", output, test.want)
			}
		})
	}
}
