package units

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// ChooseFromMenuUnit returns one validated item from a configured menu.
type ChooseFromMenuUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*ChooseFromMenuUnit)(nil)

func (u *ChooseFromMenuUnit) GetUnitName() string {
	return reflect.TypeOf(ChooseFromMenuUnit{}).Name()
}

func (u *ChooseFromMenuUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	items, err := configuredMenuItems(self)
	if err != nil {
		return nil, err
	}
	choice := items[0]
	if configured, exists := self.Params["choice"]; exists {
		text, ok := configured.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("ChooseFromMenuUnit: choice must be non-empty text")
		}
		choice = strings.TrimSpace(text)
	}
	for _, item := range items {
		if item == choice {
			return &unit.ExecutionResult{NodeName: u.UnitName, Data: choice}, nil
		}
	}
	return nil, fmt.Errorf("ChooseFromMenuUnit: choice %q is not in the configured menu", choice)
}

func configuredMenuItems(self *unit.Node) ([]string, error) {
	if self == nil || self.Params == nil {
		return nil, fmt.Errorf("ChooseFromMenuUnit: menu items are required")
	}
	raw, exists := self.Params["items"]
	if !exists {
		return nil, fmt.Errorf("ChooseFromMenuUnit: menu items are required")
	}
	var values []any
	switch items := raw.(type) {
	case []any:
		values = items
	case []string:
		values = make([]any, len(items))
		for index, item := range items {
			values[index] = item
		}
	default:
		return nil, fmt.Errorf("ChooseFromMenuUnit: items must be a list of text, got %T", raw)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("ChooseFromMenuUnit: menu items are required")
	}

	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("ChooseFromMenuUnit: item %d must be non-empty text", index+1)
		}
		text = strings.TrimSpace(text)
		if _, duplicate := seen[text]; duplicate {
			return nil, fmt.Errorf("ChooseFromMenuUnit: menu item %q is duplicated", text)
		}
		seen[text] = struct{}{}
		result = append(result, text)
	}
	return result, nil
}

func (u *ChooseFromMenuUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewChooseFromMenuUnit() ChooseFromMenuUnit {
	u := ChooseFromMenuUnit{}
	u.UnitName = u.GetUnitName()
	return u
}
