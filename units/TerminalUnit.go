package units

import (
	"context"
	"fmt"
	"reflect"

	core "github.com/ninenhan/go-workflow"
)

type TerminalUnit struct {
	core.Unit
}

var _ core.ExecutableUnit = (*TerminalUnit)(nil)

func (t *TerminalUnit) GetUnitName() string {
	return reflect.TypeOf(TerminalUnit{}).Name()
}

func (t *TerminalUnit) Execute(ctx context.Context, state core.ContextMap, self *core.Node) (*core.ExecutionResult, error) {
	return nil, fmt.Errorf("TerminalUnit %s", "执行结束")
}

func (t *TerminalUnit) GetUnitMeta() *core.Unit {
	return &t.Unit
}

func NewTerminalUnit() TerminalUnit {
	unit := TerminalUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	core.RegisterUnitFactory("TerminalUnit", func() core.ExecutableUnit {
		unit := &TerminalUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
}
