package units

import (
	"fmt"
	"github.com/dop251/goja"
	"github.com/ninenhan/go-workflow/flow"
	"reflect"
	"strings"
)

// ScriptUnit ===== ScriptUnit 动态 JS 执行单元 =====
type ScriptUnit struct {
	flow.BaseUnit
	Script string `json:"script"` // JavaScript 脚本代码
}

func (t *ScriptUnit) GetUnitName() string {
	return reflect.TypeOf(ScriptUnit{}).Name()
}

func (t *ScriptUnit) Execute(ctx *flow.PipelineContext, input *flow.Input) (*flow.Output, error) {
	vm := goja.New()
	// 注入上下文变量
	for k, v := range ctx.Env {
		_ = vm.Set("$"+k, v)
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

	if t.IOConfig == nil {
		t.IOConfig = &flow.IOConfig{}
	}

	o := &flow.Output{
		Data: result,
	}
	t.IOConfig.Output = *o
	return o, nil
}

func NewScriptUnit(script string) ScriptUnit {
	unit := ScriptUnit{
		Script: script,
	}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit := &ScriptUnit{}
	// 自动注册 HttpUnit，注意这里注册的是非指针类型
	flow.RegisterUnit(unit.GetUnitName(), unit)
}
