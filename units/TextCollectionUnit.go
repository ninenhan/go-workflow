package units

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// SplitTextUnit separates incoming text into an ordered list.
type SplitTextUnit struct {
	unit.Unit
}

// CombineTextUnit joins an incoming list into one text value.
type CombineTextUnit struct {
	unit.Unit
}

var (
	_ unit.ExecutableUnit = (*SplitTextUnit)(nil)
	_ unit.ExecutableUnit = (*CombineTextUnit)(nil)
)

func (u *SplitTextUnit) GetUnitName() string {
	return reflect.TypeOf(SplitTextUnit{}).Name()
}

func (u *SplitTextUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, fmt.Errorf("SplitTextUnit: missing text input")
	}
	text, ok := splitTextInput(self.Input.Data)
	if !ok {
		return nil, fmt.Errorf("SplitTextUnit: input must be text, got %T", self.Input.Data)
	}
	separator, mode, err := configuredTextSeparator(self.Params, "SplitTextUnit")
	if err != nil {
		return nil, err
	}
	if mode == "new_lines" {
		text = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	}
	parts := strings.Split(text, separator)
	items := make([]any, len(parts))
	for index, part := range parts {
		items[index] = part
	}
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: items}, nil
}

func splitTextInput(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case []byte:
		return string(typed), true
	default:
		return "", false
	}
}

func (u *SplitTextUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewSplitTextUnit() SplitTextUnit {
	u := SplitTextUnit{}
	u.UnitName = u.GetUnitName()
	return u
}

func (u *CombineTextUnit) GetUnitName() string {
	return reflect.TypeOf(CombineTextUnit{}).Name()
}

func (u *CombineTextUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, fmt.Errorf("CombineTextUnit: missing list input")
	}
	items, err := combinableTextItems(self.Input.Data)
	if err != nil {
		return nil, err
	}
	separator, _, err := configuredTextSeparator(self.Params, "CombineTextUnit")
	if err != nil {
		return nil, err
	}
	parts := make([]string, len(items))
	for index, item := range items {
		parts[index], err = textUnitString(item)
		if err != nil {
			return nil, fmt.Errorf("CombineTextUnit: item %d: %w", index+1, err)
		}
	}
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: strings.Join(parts, separator)}, nil
}

func combinableTextItems(value any) ([]any, error) {
	switch typed := value.(type) {
	case []any:
		return typed, nil
	case []string:
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = item
		}
		return items, nil
	default:
		return nil, fmt.Errorf("CombineTextUnit: input must be a list, got %T", value)
	}
}

func configuredTextSeparator(params map[string]any, unitName string) (separator string, mode string, err error) {
	mode = "new_lines"
	if rawMode, exists := params["separator_mode"]; exists {
		mode = strings.TrimSpace(fmt.Sprint(rawMode))
	}
	switch mode {
	case "new_lines":
		return "\n", mode, nil
	case "spaces":
		return " ", mode, nil
	case "custom":
		custom, ok := params["custom_separator"].(string)
		if !ok || custom == "" {
			return "", mode, fmt.Errorf("%s: custom_separator is required for custom mode", unitName)
		}
		return custom, mode, nil
	default:
		return "", mode, fmt.Errorf("%s: unsupported separator_mode %q", unitName, mode)
	}
}

func (u *CombineTextUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewCombineTextUnit() CombineTextUnit {
	u := CombineTextUnit{}
	u.UnitName = u.GetUnitName()
	return u
}

func init() {
	unit.RegisterUnitFactory("SplitTextUnit", func() unit.ExecutableUnit {
		u := &SplitTextUnit{}
		u.UnitName = u.GetUnitName()
		return u
	})
	unit.RegisterUnitFactory("CombineTextUnit", func() unit.ExecutableUnit {
		u := &CombineTextUnit{}
		u.UnitName = u.GetUnitName()
		return u
	})
}
