package units

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/dop251/goja"
	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// ScriptUnit ===== ScriptUnit 动态 JS 执行单元 =====
type ScriptUnit struct {
	unit.Unit
	Language string `json:"language,omitempty"`
	Script   string `json:"script"`
}

var _ unit.ExecutableUnit = (*ScriptUnit)(nil)

func (t *ScriptUnit) GetUnitName() string {
	return reflect.TypeOf(ScriptUnit{}).Name()
}

func (t *ScriptUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil {
		return nil, errors.New("ScriptUnit: missing node")
	}
	language := strings.ToLower(strings.TrimSpace(t.Language))
	if language == "" {
		language = "javascript"
	}
	if language != "javascript" {
		return nil, fmt.Errorf("ScriptUnit: unsupported language %q", t.Language)
	}
	if strings.TrimSpace(t.Script) == "" {
		return nil, errors.New("ScriptUnit: params.script is required")
	}

	vm := goja.New()
	input := any(nil)
	if self.Input != nil {
		input = self.Input.Data
	}
	if err := vm.Set("input", input); err != nil {
		return nil, fmt.Errorf("ScriptUnit: expose input: %w", err)
	}
	if err := vm.Set("$input", input); err != nil {
		return nil, fmt.Errorf("ScriptUnit: expose $input: %w", err)
	}
	for k, v := range state {
		if v == nil {
			continue
		}
		if err := vm.Set("$"+k, v.Data); err != nil {
			return nil, fmt.Errorf("ScriptUnit: expose workflow value %q: %w", k, err)
		}
	}

	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt(ctx.Err())
		case <-finished:
		}
	}()
	defaultValue, err := vm.RunString(t.Script)
	close(finished)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("ScriptUnit: execution interrupted: %w", ctxErr)
		}
		return nil, fmt.Errorf("ScriptUnit: execute JavaScript: %w", err)
	}

	result := make(map[string]any)
	global := vm.GlobalObject()
	keys := global.Keys()
	result["$$"] = defaultValue.Export()
	for _, key := range keys {
		if strings.HasPrefix(key, "$$") {
			val := vm.Get(key)
			result[key] = val.Export()
		}
	}

	return &unit.ExecutionResult{
		NodeName: t.UnitName,
		Data:     result,
	}, nil
}

func (t *ScriptUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewScriptUnit(script string) ScriptUnit {
	unit := ScriptUnit{
		Language: "javascript",
		Script:   script,
	}
	unit.UnitName = unit.GetUnitName()
	return unit
}
