package units

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// GetListItemUnit selects one value from an incoming list.
type GetListItemUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*GetListItemUnit)(nil)

func (u *GetListItemUnit) GetUnitName() string {
	return reflect.TypeOf(GetListItemUnit{}).Name()
}

func (u *GetListItemUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	items, err := inputListItems(self)
	if err != nil {
		return nil, err
	}
	index, err := selectedListIndex(self, len(items))
	if err != nil {
		return nil, err
	}
	return &unit.ExecutionResult{
		NodeName: u.UnitName,
		Data:     items[index],
	}, nil
}

func inputListItems(self *unit.Node) ([]any, error) {
	if self == nil || self.Input == nil {
		return nil, fmt.Errorf("GetListItemUnit: missing list input")
	}
	switch items := self.Input.Data.(type) {
	case []any:
		if len(items) == 0 {
			return nil, fmt.Errorf("GetListItemUnit: input list is empty")
		}
		return items, nil
	case []string:
		if len(items) == 0 {
			return nil, fmt.Errorf("GetListItemUnit: input list is empty")
		}
		result := make([]any, len(items))
		for index, item := range items {
			result[index] = item
		}
		return result, nil
	default:
		return nil, fmt.Errorf("GetListItemUnit: input must be a list, got %T", self.Input.Data)
	}
}

func selectedListIndex(self *unit.Node, length int) (int, error) {
	operation := "first"
	if self != nil && self.Params != nil {
		if value, exists := self.Params["operation"]; exists {
			operation = strings.TrimSpace(fmt.Sprint(value))
		}
	}
	switch operation {
	case "first":
		return 0, nil
	case "last":
		return length - 1, nil
	case "random":
		return rand.IntN(length), nil
	case "index":
		position, err := configuredListPosition(self)
		if err != nil {
			return 0, err
		}
		if position > length {
			return 0, fmt.Errorf("GetListItemUnit: item_number %d exceeds list length %d", position, length)
		}
		return position - 1, nil
	default:
		return 0, fmt.Errorf("GetListItemUnit: unsupported operation %q", operation)
	}
}

func configuredListPosition(self *unit.Node) (int, error) {
	if self == nil || self.Params == nil {
		return 0, fmt.Errorf("GetListItemUnit: item_number is required for index mode")
	}
	value, exists := self.Params["item_number"]
	if !exists {
		return 0, fmt.Errorf("GetListItemUnit: item_number is required for index mode")
	}
	position, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
	if err != nil || position < 1 {
		return 0, fmt.Errorf("GetListItemUnit: item_number must be a positive integer")
	}
	return position, nil
}

func (u *GetListItemUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewGetListItemUnit() GetListItemUnit {
	u := GetListItemUnit{}
	u.UnitName = u.GetUnitName()
	return u
}
