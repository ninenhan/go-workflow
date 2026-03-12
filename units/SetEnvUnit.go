package units

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

type SetEnvUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*SetEnvUnit)(nil)

func (t *SetEnvUnit) GetUnitName() string {
	return reflect.TypeOf(SetEnvUnit{}).Name()
}

func (t *SetEnvUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
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
	return &unit.ExecutionResult{
		NodeName: t.UnitName,
		Data:     mapData,
	}, nil
}

func (t *SetEnvUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewSetEnvUnit() SetEnvUnit {
	unit := SetEnvUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit.RegisterUnitFactory("SetEnvUnit", func() unit.ExecutableUnit {
		unit := &SetEnvUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
}
