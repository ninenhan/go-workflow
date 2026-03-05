package units

import core "github.com/ninenhan/go-workflow"

func AutoRegister() {
	core.RegisterUnitFactory("HttpUnit", func() core.ExecutableUnit {
		unit := &HttpUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
}
