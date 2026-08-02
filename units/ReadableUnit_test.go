package units

import (
	"context"
	"strings"
	"testing"

	coreexecutor "github.com/ninenhan/go-workflow/core/executor"
	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

func newReadableUnitExecutor() *workerunit.Executor {
	registry := workerunit.NewRegistry()
	registry.RegisterUnitFactory("ReadableUnit", func() workerunit.ExecutableUnit {
		action := &ReadableUnit{Format: "text"}
		action.UnitName = action.GetUnitName()
		return action
	})
	return workerunit.NewExecutor(registry)
}

func executeReadableUnit(t *testing.T, input any, params map[string]any) (any, error) {
	t.Helper()
	result, err := newReadableUnitExecutor().Execute(context.Background(), coreexecutor.ExecuteTask{
		RunID:        "run-readable",
		NodeID:       "readable",
		ExecutorType: string(coreexecutor.TypeUnit),
		ExecutorRef:  "ReadableUnit",
		Input:        input,
		Params:       params,
	})
	return result.Output, err
}

func TestReadableUnitFormatsTextJSONAndTemplate(t *testing.T) {
	for name, test := range map[string]struct {
		input  any
		params map[string]any
		want   string
	}{
		"text": {
			input: "hello",
			want:  "hello",
		},
		"structured text": {
			input: map[string]any{"name": "Ada"},
			want:  "{\n  \"name\": \"Ada\"\n}",
		},
		"json string": {
			input:  "hello",
			params: map[string]any{"format": "json"},
			want:   "\"hello\"",
		},
		"template": {
			input:  map[string]any{"message": "Ready"},
			params: map[string]any{"format": "text", "template": "Status: {{.message}}"},
			want:   "Status: Ready",
		},
	} {
		t.Run(name, func(t *testing.T) {
			output, err := executeReadableUnit(t, test.input, test.params)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if output != test.want {
				t.Fatalf("output = %#v, want %#v", output, test.want)
			}
		})
	}
}

func TestReadableUnitRejectsInvalidConfigurationAndData(t *testing.T) {
	for name, test := range map[string]struct {
		input  any
		params map[string]any
	}{
		"unknown format": {
			input:  "hello",
			params: map[string]any{"format": "xml"},
		},
		"template with json": {
			input:  map[string]any{"message": "Ready"},
			params: map[string]any{"format": "json", "template": "{{.message}}"},
		},
		"missing template value": {
			input:  map[string]any{"other": "Ready"},
			params: map[string]any{"format": "text", "template": "{{.message}}"},
		},
		"unsupported json value": {
			input: make(chan int),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := executeReadableUnit(t, test.input, test.params); err == nil ||
				!strings.HasPrefix(err.Error(), "ReadableUnit:") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
