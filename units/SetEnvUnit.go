package units

import (
	"encoding/json"
	"errors"
	"github.com/ninenhan/go-workflow/flow"
	"reflect"
)

type SetEnvUnit struct {
	flow.BaseUnit
}

func (t *SetEnvUnit) GetUnitName() string {
	return reflect.TypeOf(SetEnvUnit{}).Name()
}

func (t *SetEnvUnit) Execute(ctx *flow.PipelineContext, i *flow.Input) (*flow.Output, error) {
	if t.IOConfig == nil {
		t.IOConfig = &flow.IOConfig{}
	}
	var mapData = make(map[string]any)
	if v, ok := i.Data.(map[string]any); ok {
		mapData = v
	} else {
		if v, ok := i.Data.(string); ok {
			err := json.Unmarshal([]byte(v), &mapData)
			if err != nil {
				return nil, errors.New("SetEnvUnit Error")
			}
		}
	}
	for k, v := range mapData {
		ctx.SetEnv(k, v)
	}
	o := &flow.Output{
		Data: mapData,
	}
	t.IOConfig.Output = *o
	return o, nil
}

func NewSetEnvUnit() SetEnvUnit {
	unit := SetEnvUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit := &SetEnvUnit{}
	// 自动注册 HttpUnit，注意这里注册的是非指针类型
	flow.RegisterUnit(unit.GetUnitName(), unit)
}
