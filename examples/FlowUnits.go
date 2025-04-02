package main

import (
	"encoding/json"
	"fmt"
	"github.com/ninenhan/go-workflow/flow"
	"github.com/ninenhan/go-workflow/units"
)

func FlowUnits() {
	var unitList []flow.PhaseUnit
	{
		printUnit := units.NewLogUnit()
		printUnit.ID = "Yw5pIVw9"
		printUnit.IOConfig = &flow.IOConfig{
			//InputKey: "input123",
			DefaultInput: flow.Input{Data: "你好，这是一个LogUnit", DataType: "", Slottable: false},
		}
		unitList = append(unitList, &printUnit)
	}

	{
		printUnit := units.NewTimeoutUnit()
		printUnit.ID = "1000pe30"
		printUnit.IOConfig = &flow.IOConfig{
			//InputKey: "input123",
			Input: flow.Input{Data: "{{_:3000}}", DataType: "", Slottable: true},
		}
		unitList = append(unitList, &printUnit)
	}

	{
		printUnit := units.NewIfUnit()
		printUnit.ID = "297hjf71"
		printUnit.IfCondition = flow.Condition{
			Key:      "{{Yw5pIVw9.output}}",
			Operator: "LIKE",
			Value:    "追加",
		}

		un1 := units.NewLogUnit()
		un1.ID = "ew5pEmfk"
		un1.IOConfig = &flow.IOConfig{
			//InputKey: "input123",
			DefaultInput: flow.Input{Data: "你好Unit", DataType: "", Slottable: false},
		}
		printUnit.IfUnits = []flow.PhaseUnit{&un1}
		unitList = append(unitList, &printUnit)
	}

	{
		printUnit := units.NewLogUnit()
		printUnit.ID = "1g9OpkX0"
		printUnit.IOConfig = &flow.IOConfig{
			//InputKey: "input123",
			Input: flow.Input{Data: "UUU! {{ew5pEmfk.output}}", DataType: "", Slottable: true},
		}
		unitList = append(unitList, &printUnit)
	}

	{
		printUnit := units.NewScriptUnit("const a = 10 + 1; $$b = a * 2; $$status = $Yw5pIVw9.output")
		printUnit.ID = "40991fj1"
		printUnit.IOConfig = &flow.IOConfig{}
		unitList = append(unitList, &printUnit)
	}

	{
		printUnit := units.NewLogUnit()
		printUnit.ID = "871hf761"
		printUnit.IOConfig = &flow.IOConfig{
			Input: flow.Input{Data: "追加! {{40991fj1.output.$$b}}", DataType: "", Slottable: true},
		}
		unitList = append(unitList, &printUnit)
	}

	jsonData, err := json.MarshalIndent(unitList, "", "  ")
	if err != nil {
		fmt.Printf("序列化错误: %v\n", err)
	} else {
		fmt.Printf("序列化后的 pipeline 单元:\n%s\n", string(jsonData))
	}

	////反序列化
	//e := json.Unmarshal(jsonData, &units)

	unit, e := flow.ParsePhaseUnits(jsonData, "")
	if e != nil {
		fmt.Printf("反序列化错误: %v\n", e)
	} else {
		fmt.Printf("反序列化后的 pipeline 单元:\n%v\n", unit)
	}

	// 构造 pipeline（Storyboard 可理解为对各单元的配置与连线）
	pipeline := flow.NewPipeline(unit)
	//
	// 运行 pipeline
	if err := pipeline.Run(); err != nil {
		fmt.Printf("Pipeline 执行错误: %v\n", err)
	}

	// 序列化 pipeline 单元（用于保存配置或故事板）
	res, err := json.MarshalIndent(pipeline.Units, "", "  ")
	if err != nil {
		fmt.Printf("序列化错误: %v\n", err)
	} else {
		fmt.Printf("序列化后的 pipeline 单元:\n%s\n", string(res))
	}

}
