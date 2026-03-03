package units

import (
	"context"
	"reflect"

	core "github.com/ninenhan/go-workflow"
)

type BreakUnit struct {
	core.Unit
}

var _ core.ExecutableUnit = (*BreakUnit)(nil)

func (u *BreakUnit) GetUnitName() string {
	return reflect.TypeOf(BreakUnit{}).Name()
}

func (u *BreakUnit) Execute(ctx context.Context, state core.ContextMap, self *core.Node) (*core.ExecutionResult, error) {
	return &core.ExecutionResult{
		NodeName: u.UnitName,
		Control:  core.ControlBreak,
	}, nil
}

func (u *BreakUnit) GetUnitMeta() *core.Unit {
	return &u.Unit
}

type ContinueUnit struct {
	core.Unit
}

var _ core.ExecutableUnit = (*ContinueUnit)(nil)

func (u *ContinueUnit) GetUnitName() string {
	return reflect.TypeOf(ContinueUnit{}).Name()
}

func (u *ContinueUnit) Execute(ctx context.Context, state core.ContextMap, self *core.Node) (*core.ExecutionResult, error) {
	return &core.ExecutionResult{
		NodeName: u.UnitName,
		Control:  core.ControlContinue,
	}, nil
}

func (u *ContinueUnit) GetUnitMeta() *core.Unit {
	return &u.Unit
}

type GotoUnit struct {
	core.Unit
}

var _ core.ExecutableUnit = (*GotoUnit)(nil)

func (u *GotoUnit) GetUnitName() string {
	return reflect.TypeOf(GotoUnit{}).Name()
}

func (u *GotoUnit) Execute(ctx context.Context, state core.ContextMap, self *core.Node) (*core.ExecutionResult, error) {
	return &core.ExecutionResult{
		NodeName: u.UnitName,
		Control:  core.ControlGoto,
	}, nil
}

func (u *GotoUnit) GetUnitMeta() *core.Unit {
	return &u.Unit
}

func init() {
	{
		unit := &BreakUnit{}
		core.RegisterUnit(unit.GetUnitName(), unit)
	}
	{
		unit := &ContinueUnit{}
		core.RegisterUnit(unit.GetUnitName(), unit)
	}
}
