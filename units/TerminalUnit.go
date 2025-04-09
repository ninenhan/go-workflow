package units

import (
	"fmt"
	"github.com/ninenhan/go-workflow/flow"
	"reflect"
)

type TerminalUnit struct {
	flow.BaseUnit
}

func (t *TerminalUnit) GetUnitName() string {
	return reflect.TypeOf(TerminalUnit{}).Name()
}

func (t *TerminalUnit) Execute(ctx *flow.PipelineContext, i *flow.Input) (*flow.Output, error) {
	return nil, fmt.Errorf("TerminalUnit %s", "执行结束")
}

func NewTerminalUnit() TerminalUnit {
	unit := TerminalUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit := &TerminalUnit{}
	// 自动注册 HttpUnit，注意这里注册的是非指针类型
	flow.RegisterUnit(unit.GetUnitName(), unit)
}
