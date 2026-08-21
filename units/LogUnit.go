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
	return newLogicAlias("LogUnit")
}

func newLogicAlias(name string) LogicUnit {
	action := LogicUnit{}
	action.UnitName = name
	return action
}

func logicAliasFactory(name string) unit.Factory {
	return func() unit.ExecutableUnit {
		action := newLogicAlias(name)
		return &action
	}
}
