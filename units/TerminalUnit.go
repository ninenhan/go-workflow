package units

import (
	"context"
	"fmt"
	"reflect"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

type TerminalUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*TerminalUnit)(nil)

func (t *TerminalUnit) GetUnitName() string {
	return reflect.TypeOf(TerminalUnit{}).Name()
}

func (t *TerminalUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	return nil, fmt.Errorf("TerminalUnit %s", "执行结束")
}

func (t *TerminalUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewTerminalUnit() TerminalUnit {
	unit := TerminalUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit.RegisterUnitFactory("TerminalUnit", func() unit.ExecutableUnit {
		unit := &TerminalUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
}
