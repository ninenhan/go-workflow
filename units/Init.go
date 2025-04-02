package units

import (
	"github.com/ninenhan/go-workflow/flow"
)

func NewIfUnit() flow.IfUnit {
	unit := flow.IfUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}
func NewWhileUnit() flow.WhileUnit {
	unit := flow.WhileUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	{
		unit := &flow.IfUnit{}
		flow.RegisterUnit(unit.GetUnitName(), unit)
	}
	{
		unit := &flow.WhileUnit{}
		flow.RegisterUnit(unit.GetUnitName(), unit)
	}
}
