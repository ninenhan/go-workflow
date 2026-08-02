package units

import (
	"context"
	"fmt"
	"reflect"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// ListUnit returns configured items in their declared order.
type ListUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*ListUnit)(nil)

func (u *ListUnit) GetUnitName() string {
	return reflect.TypeOf(ListUnit{}).Name()
}

func (u *ListUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	items, err := configuredListItems(self)
	if err != nil {
		return nil, err
	}
	return &unit.ExecutionResult{
		NodeName: u.UnitName,
		Data:     items,
	}, nil
}

func configuredListItems(self *unit.Node) ([]any, error) {
	if self == nil || self.Params == nil {
		return []any{}, nil
	}
	value, exists := self.Params["items"]
	if !exists || value == nil {
		return []any{}, nil
	}
	switch items := value.(type) {
	case []any:
		return append([]any(nil), items...), nil
	case []string:
		result := make([]any, len(items))
		for index, item := range items {
			result[index] = item
		}
		return result, nil
	default:
		return nil, fmt.Errorf("ListUnit: items must be a list, got %T", value)
	}
}

func (u *ListUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewListUnit() ListUnit {
	u := ListUnit{}
	u.UnitName = u.GetUnitName()
	return u
}

func init() {
	unit.RegisterUnitFactory("ListUnit", func() unit.ExecutableUnit {
		u := &ListUnit{}
		u.UnitName = u.GetUnitName()
		return u
	})
}
