package units

import (
	"github.com/ninenhan/go-workflow/flow"
	"reflect"
)

type LogicUnit struct {
	flow.BaseUnit
}

func (t *LogicUnit) GetUnitName() string {
	return reflect.TypeOf(LogicUnit{}).Name()
}

func (t *LogicUnit) Execute(ctx *flow.PipelineContext, i *flow.Input) (*flow.Output, error) {
	if t.IOConfig == nil {
		t.IOConfig = &flow.IOConfig{}
	}
	o := &flow.Output{
		Data: i.Data,
	}
	t.IOConfig.Output = *o
	return o, nil
}

func NewLogUnit() LogicUnit {
	unit := LogicUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit := &LogicUnit{}
	// 自动注册 HttpUnit，注意这里注册的是非指针类型
	flow.RegisterUnit(unit.GetUnitName(), unit)
}
