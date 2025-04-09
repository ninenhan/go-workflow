package units

import (
	"github.com/ninenhan/go-workflow/flow"
	"reflect"
)

type RemarkUnit struct {
	flow.BaseUnit
}

func (t *RemarkUnit) GetUnitName() string {
	return reflect.TypeOf(RemarkUnit{}).Name()
}

func (t *RemarkUnit) Execute(ctx *flow.PipelineContext, i *flow.Input) (*flow.Output, error) {
	if t.IOConfig == nil {
		t.IOConfig = &flow.IOConfig{}
	}
	o := &flow.Output{
		Data: i.Data,
	}
	t.IOConfig.Output = *o
	return o, nil
}

func NewRemarkUnit() RemarkUnit {
	unit := RemarkUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit := &RemarkUnit{}
	// 自动注册 HttpUnit，注意这里注册的是非指针类型
	flow.RegisterUnit(unit.GetUnitName(), unit)
}
