package units

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// TextUnit creates text from a fixed value or a resolved Magic Variable.
type TextUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*TextUnit)(nil)

func (u *TextUnit) GetUnitName() string {
	return reflect.TypeOf(TextUnit{}).Name()
}

func (u *TextUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil {
		return nil, fmt.Errorf("TextUnit: missing node")
	}
	text, err := textUnitString(self.Params["text"])
	if err != nil {
		return nil, err
	}
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: text}, nil
}

func textUnitString(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	case bool, float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(typed), nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("TextUnit: encode text value: %w", err)
		}
		return string(encoded), nil
	}
}

func (u *TextUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewTextUnit() TextUnit {
	u := TextUnit{}
	u.UnitName = u.GetUnitName()
	return u
}

func init() {
	unit.RegisterUnitFactory("TextUnit", func() unit.ExecutableUnit {
		u := &TextUnit{}
		u.UnitName = u.GetUnitName()
		return u
	})
}
