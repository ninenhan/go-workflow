package main

import (
	core "github.com/ninenhan/go-workflow"
	"github.com/ninenhan/go-workflow/units"
)

func main() {
	//fn.ParseTemplateTest()
	//FlowUnitsTests()
	//AccessNetWorkTests()
	//DagGraphTests()
	units.AutoRegister()
	core.Test_Json_To_Graph()
}
