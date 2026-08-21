package units

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// DictionaryUnit builds a typed dictionary from visually configured entries.
type DictionaryUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*DictionaryUnit)(nil)

func (u *DictionaryUnit) GetUnitName() string {
	return reflect.TypeOf(DictionaryUnit{}).Name()
}

func (u *DictionaryUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	entries, err := configuredDictionaryEntries(self)
	if err != nil {
		return nil, err
	}
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: entries}, nil
}

func configuredDictionaryEntries(self *unit.Node) (map[string]any, error) {
	if self == nil || self.Params == nil || self.Params["entries"] == nil {
		return map[string]any{}, nil
	}
	return configuredTypedEntries("DictionaryUnit", "entries", self.Params["entries"])
}

func configuredTypedEntries(unitName, paramName string, raw any) (map[string]any, error) {
	result := map[string]any{}
	if raw == nil {
		return result, nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s must be a list, got %T", unitName, paramName, raw)
	}
	for index, rawEntry := range entries {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: %s entry %d must be an object, got %T", unitName, paramName, index+1, rawEntry)
		}
		rawKey, keyExists := entry["key"]
		key := ""
		if keyExists && rawKey != nil {
			key = strings.TrimSpace(fmt.Sprint(rawKey))
		}
		if key == "" {
			return nil, fmt.Errorf("%s: %s entry %d key is required", unitName, paramName, index+1)
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("%s: %s has duplicate key %q", unitName, paramName, key)
		}
		value, err := dictionaryEntryValue(unitName, paramName, entry, index)
		if err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}

func dictionaryEntryValue(unitName, paramName string, entry map[string]any, index int) (any, error) {
	valueType := "text"
	if rawType, exists := entry["type"]; exists && rawType != nil {
		if configuredType := strings.TrimSpace(fmt.Sprint(rawType)); configuredType != "" {
			valueType = configuredType
		}
	}
	value := entry["value"]
	switch valueType {
	case "text":
		if value == nil {
			return "", nil
		}
		return fmt.Sprint(value), nil
	case "number":
		number, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("%s: %s entry %d value must be a finite number", unitName, paramName, index+1)
		}
		return number, nil
	case "boolean":
		if boolean, ok := value.(bool); ok {
			return boolean, nil
		}
		boolean, err := strconv.ParseBool(strings.TrimSpace(fmt.Sprint(value)))
		if err != nil {
			return nil, fmt.Errorf("%s: %s entry %d value must be true or false", unitName, paramName, index+1)
		}
		return boolean, nil
	default:
		return nil, fmt.Errorf("%s: %s entry %d has unsupported type %q", unitName, paramName, index+1, valueType)
	}
}

func (u *DictionaryUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewDictionaryUnit() DictionaryUnit {
	u := DictionaryUnit{}
	u.UnitName = u.GetUnitName()
	return u
}
