package units

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	coreexecutor "github.com/ninenhan/go-workflow/core/executor"
	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

func newScriptUnitExecutor() *workerunit.Executor {
	registry := workerunit.NewRegistry()
	registry.RegisterUnitFactory("ScriptUnit", func() workerunit.ExecutableUnit {
		action := &ScriptUnit{Language: "javascript"}
		action.UnitName = action.GetUnitName()
		return action
	})
	return workerunit.NewExecutor(registry)
}

func TestScriptUnitExecutesHydratedJavaScriptContract(t *testing.T) {
	result, err := newScriptUnitExecutor().Execute(context.Background(), coreexecutor.ExecuteTask{
		RunID:        "run-script",
		NodeID:       "script",
		ExecutorType: string(coreexecutor.TypeUnit),
		ExecutorRef:  "ScriptUnit",
		Input:        map[string]any{"name": "Ada", "count": 2},
		Context:      map[string]any{"previous": "ready"},
		Params: map[string]any{
			"language": "javascript",
			"script": `
const prior = globalThis["$previous"];
$$message = input.name + ":" + prior;
({ count: input.count + 1 });
`,
		},
	})
	if err != nil {
		t.Fatalf("execute script: %v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v", result.Output)
	}
	if output["$$message"] != "Ada:ready" {
		t.Fatalf("exported field = %#v", output["$$message"])
	}
	whole, ok := output["$$"].(map[string]any)
	if !ok || whole["count"] != int64(3) {
		t.Fatalf("whole output = %#v", output["$$"])
	}
}

func TestScriptUnitRejectsUnsupportedLanguageAndMissingSource(t *testing.T) {
	executor := newScriptUnitExecutor()
	for name, params := range map[string]map[string]any{
		"unsupported language": {"language": "python", "script": "1 + 1"},
		"missing source":       {"language": "javascript", "script": " \n "},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := executor.Execute(context.Background(), coreexecutor.ExecuteTask{
				RunID:        "run-script",
				NodeID:       "script",
				ExecutorType: string(coreexecutor.TypeUnit),
				ExecutorRef:  "ScriptUnit",
				Params:       params,
			})
			if err == nil || !strings.HasPrefix(err.Error(), "ScriptUnit:") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestScriptUnitInterruptsInfiniteLoopWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := newScriptUnitExecutor().Execute(ctx, coreexecutor.ExecuteTask{
		RunID:        "run-script",
		NodeID:       "script",
		ExecutorType: string(coreexecutor.TypeUnit),
		ExecutorRef:  "ScriptUnit",
		Params: map[string]any{
			"language": "javascript",
			"script":   "for (;;) {}",
		},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("script interruption took %s", elapsed)
	}
}
