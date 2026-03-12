package units

import (
	"context"
	"reflect"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

type RemarkUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*RemarkUnit)(nil)

func (t *RemarkUnit) GetUnitName() string {
	return reflect.TypeOf(RemarkUnit{}).Name()
}

func (t *RemarkUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return &unit.ExecutionResult{NodeName: t.UnitName}, nil
	}
	return &unit.ExecutionResult{
		NodeName: t.UnitName,
		Data:     self.Input.Data,
	}, nil
}

func (t *RemarkUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewRemarkUnit() RemarkUnit {
	unit := RemarkUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit.RegisterUnitFactory("RemarkUnit", func() unit.ExecutableUnit {
		unit := &RemarkUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
}
