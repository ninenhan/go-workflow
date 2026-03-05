package units

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	core "github.com/ninenhan/go-workflow"
)

type SetEnvUnit struct {
	core.Unit
}

var _ core.ExecutableUnit = (*SetEnvUnit)(nil)

func (t *SetEnvUnit) GetUnitName() string {
	return reflect.TypeOf(SetEnvUnit{}).Name()
}

func (t *SetEnvUnit) Execute(ctx context.Context, state core.ContextMap, self *core.Node) (*core.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, errors.New("SetEnvUnit: missing input")
	}
	var mapData = make(map[string]any)
	if v, ok := self.Input.Data.(map[string]any); ok {
		mapData = v
	} else if v, ok := self.Input.Data.(string); ok {
		if err := json.Unmarshal([]byte(v), &mapData); err != nil {
			return nil, errors.New("SetEnvUnit: invalid json")
		}
	}
	return &core.ExecutionResult{
		NodeName: t.UnitName,
		Data:     mapData,
	}, nil
}

func (t *SetEnvUnit) GetUnitMeta() *core.Unit {
	return &t.Unit
}

func NewSetEnvUnit() SetEnvUnit {
	unit := SetEnvUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	core.RegisterUnitFactory("SetEnvUnit", func() core.ExecutableUnit {
		unit := &SetEnvUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
}
