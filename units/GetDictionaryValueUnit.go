package units

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// GetDictionaryValueUnit returns the value stored at an explicitly configured key.
type GetDictionaryValueUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*GetDictionaryValueUnit)(nil)

func (u *GetDictionaryValueUnit) GetUnitName() string {
	return reflect.TypeOf(GetDictionaryValueUnit{}).Name()
}

func (u *GetDictionaryValueUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	key, err := configuredDictionaryKey(self)
	if err != nil {
		return nil, err
	}
	value, exists, err := dictionaryValueAtKey(self, key)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("GetDictionaryValueUnit: key %q does not exist", key)
	}
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: value}, nil
}

func configuredDictionaryKey(self *unit.Node) (string, error) {
	if self == nil || self.Params == nil {
		return "", fmt.Errorf("GetDictionaryValueUnit: key is required")
	}
	rawKey, exists := self.Params["key"]
	if !exists || rawKey == nil {
		return "", fmt.Errorf("GetDictionaryValueUnit: key is required")
	}
	key := strings.TrimSpace(fmt.Sprint(rawKey))
	if key == "" {
		return "", fmt.Errorf("GetDictionaryValueUnit: key is required")
	}
	return key, nil
}

func dictionaryValueAtKey(self *unit.Node, key string) (any, bool, error) {
	if self == nil || self.Input == nil {
		return nil, false, fmt.Errorf("GetDictionaryValueUnit: missing dictionary input")
	}
	switch dictionary := self.Input.Data.(type) {
	case map[string]any:
		value, exists := dictionary[key]
		return value, exists, nil
	case map[string]string:
		value, exists := dictionary[key]
		return value, exists, nil
	default:
		return nil, false, fmt.Errorf("GetDictionaryValueUnit: input must be a dictionary, got %T", self.Input.Data)
	}
}

func (u *GetDictionaryValueUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewGetDictionaryValueUnit() GetDictionaryValueUnit {
	u := GetDictionaryValueUnit{}
	u.UnitName = u.GetUnitName()
	return u
}
