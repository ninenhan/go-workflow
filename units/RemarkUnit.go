package units

import (
	"context"
	"reflect"

	core "github.com/ninenhan/go-workflow"
)

type RemarkUnit struct {
	core.Unit
}

var _ core.ExecutableUnit = (*RemarkUnit)(nil)

func (t *RemarkUnit) GetUnitName() string {
	return reflect.TypeOf(RemarkUnit{}).Name()
}

func (t *RemarkUnit) Execute(ctx context.Context, state core.ContextMap, self *core.Node) (*core.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return &core.ExecutionResult{NodeName: t.UnitName}, nil
	}
	return &core.ExecutionResult{
		NodeName: t.UnitName,
		Data:     self.Input.Data,
	}, nil
}

func (t *RemarkUnit) GetUnitMeta() *core.Unit {
	return &t.Unit
}

func NewRemarkUnit() RemarkUnit {
	unit := RemarkUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	core.RegisterUnitFactory("RemarkUnit", func() core.ExecutableUnit {
		unit := &RemarkUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
}
