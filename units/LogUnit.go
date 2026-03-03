package units

import (
	"context"
	"reflect"

	core "github.com/ninenhan/go-workflow"
)

type LogicUnit struct {
	core.Unit
}

var _ core.ExecutableUnit = (*LogicUnit)(nil)

func (t *LogicUnit) GetUnitName() string {
	return reflect.TypeOf(LogicUnit{}).Name()
}

func (t *LogicUnit) Execute(ctx context.Context, state core.ContextMap, self *core.Node) (*core.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return &core.ExecutionResult{NodeName: t.UnitName}, nil
	}
	return &core.ExecutionResult{
		NodeName: t.UnitName,
		Data:     self.Input.Data,
	}, nil
}

func (t *LogicUnit) GetUnitMeta() *core.Unit {
	return &t.Unit
}

func NewLogUnit() LogicUnit {
	unit := LogicUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit := &LogicUnit{}
	core.RegisterUnit(unit.GetUnitName(), unit)
}
