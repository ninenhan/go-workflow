package units

import core "github.com/ninenhan/go-workflow"

func AutoRegister() {
	{
		unit := &HttpUnit{}
		// 自动注册 HttpUnit，注意这里注册的是非指针类型
		core.RegisterUnit(unit.GetUnitName(), unit)
	}
}
