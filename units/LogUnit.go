package units

import (
	"context"
	"reflect"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

type LogicUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*LogicUnit)(nil)

func (t *LogicUnit) GetUnitName() string {
	return reflect.TypeOf(LogicUnit{}).Name()
}

func (t *LogicUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return &unit.ExecutionResult{NodeName: t.UnitName}, nil
	}
	return &unit.ExecutionResult{
		NodeName: t.UnitName,
		Data:     self.Input.Data,
	}, nil
}

func (t *LogicUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewLogUnit() LogicUnit {
	unit := LogicUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	factory := func() unit.ExecutableUnit {
		unit := &LogicUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	}
	unit.RegisterUnitFactory("LogicUnit", factory)
	unit.RegisterUnitFactory("LogUnit", factory)
}
