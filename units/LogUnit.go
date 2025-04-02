package units

import (
	"github.com/ninenhan/go-workflow/flow"
	"reflect"
)

type LogUnit struct {
	flow.BaseUnit
}

func (t *LogUnit) GetUnitName() string {
	return reflect.TypeOf(LogUnit{}).Name()
}

func (t *LogUnit) Execute(ctx *flow.PipelineContext, i *flow.Input) (*flow.Output, error) {
	if t.IOConfig == nil {
		t.IOConfig = &flow.IOConfig{}
	}

	o := &flow.Output{
		Data: i.Data,
	}
	t.IOConfig.Output = *o
	return o, nil
}

func NewLogUnit() LogUnit {
	unit := LogUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit := &LogUnit{}
	// 自动注册 HttpUnit，注意这里注册的是非指针类型
	flow.RegisterUnit(unit.GetUnitName(), unit)
}
