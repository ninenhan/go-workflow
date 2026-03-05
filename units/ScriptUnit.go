package units

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/dop251/goja"
	core "github.com/ninenhan/go-workflow"
)

// ScriptUnit ===== ScriptUnit 动态 JS 执行单元 =====
type ScriptUnit struct {
	core.Unit
	Script string `json:"script"` // JavaScript 脚本代码
}

var _ core.ExecutableUnit = (*ScriptUnit)(nil)

func (t *ScriptUnit) GetUnitName() string {
	return reflect.TypeOf(ScriptUnit{}).Name()
}

func (t *ScriptUnit) Execute(ctx context.Context, state core.ContextMap, self *core.Node) (*core.ExecutionResult, error) {
	vm := goja.New()
	// 注入上下文变量
	for k, v := range state {
		if v == nil {
			continue
		}
		_ = vm.Set("$"+k, v.Data)
	}
	defaultValue, err := vm.RunString(t.Script)
	if err != nil {
		return nil, fmt.Errorf("ScriptUnit 执行失败: %w", err)
	}
	// 自动收集全局变量
	result := make(map[string]any)
	global := vm.GlobalObject()
	keys := global.Keys()
	result["$$"] = defaultValue
	for _, key := range keys {
		if strings.HasPrefix(key, "$$") {
			val := vm.Get(key)
			result[key] = val.Export()
		}
	}

	return &core.ExecutionResult{
		NodeName: t.UnitName,
		Data:     result,
	}, nil
}

func (t *ScriptUnit) GetUnitMeta() *core.Unit {
	return &t.Unit
}

func NewScriptUnit(script string) ScriptUnit {
	unit := ScriptUnit{
		Script: script,
	}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	core.RegisterUnitFactory("ScriptUnit", func() core.ExecutableUnit {
		unit := &ScriptUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
}
