package units

import (
	"context"
	"reflect"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

type BreakUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*BreakUnit)(nil)

func (u *BreakUnit) GetUnitName() string {
	return reflect.TypeOf(BreakUnit{}).Name()
}

func (u *BreakUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	return &unit.ExecutionResult{
		NodeName: u.UnitName,
		Control:  unit.ControlBreak,
	}, nil
}

func (u *BreakUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

type ContinueUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*ContinueUnit)(nil)

func (u *ContinueUnit) GetUnitName() string {
	return reflect.TypeOf(ContinueUnit{}).Name()
}

func (u *ContinueUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	return &unit.ExecutionResult{
		NodeName: u.UnitName,
		Control:  unit.ControlContinue,
	}, nil
}

func (u *ContinueUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

type GotoUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*GotoUnit)(nil)

func (u *GotoUnit) GetUnitName() string {
	return reflect.TypeOf(GotoUnit{}).Name()
}

func (u *GotoUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	return &unit.ExecutionResult{
		NodeName: u.UnitName,
		Control:  unit.ControlGoto,
	}, nil
}

func (u *GotoUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}
