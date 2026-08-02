package units

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// ReplaceTextUnit replaces literal text in the incoming value.
type ReplaceTextUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*ReplaceTextUnit)(nil)

func (u *ReplaceTextUnit) GetUnitName() string {
	return reflect.TypeOf(ReplaceTextUnit{}).Name()
}

func (u *ReplaceTextUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, fmt.Errorf("ReplaceTextUnit: missing text input")
	}
	input, ok := splitTextInput(self.Input.Data)
	if !ok {
		return nil, fmt.Errorf("ReplaceTextUnit: input must be text, got %T", self.Input.Data)
	}
	find, err := replaceTextParameter(self.Params["find"], "find")
	if err != nil {
		return nil, err
	}
	if find == "" {
		return nil, fmt.Errorf("ReplaceTextUnit: find must not be empty")
	}
	replacement, err := replaceTextParameter(self.Params["replacement"], "replacement")
	if err != nil {
		return nil, err
	}
	caseSensitive, err := replaceTextBool(self.Params, "case_sensitive", true)
	if err != nil {
		return nil, err
	}
	replaceAll, err := replaceTextBool(self.Params, "replace_all", true)
	if err != nil {
		return nil, err
	}

	result := replaceLiteralText(input, find, replacement, caseSensitive, replaceAll)
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: result}, nil
}

func replaceTextParameter(value any, name string) (string, error) {
	if value == nil {
		return "", nil
	}
	text, ok := splitTextInput(value)
	if !ok {
		return "", fmt.Errorf("ReplaceTextUnit: %s must be text, got %T", name, value)
	}
	return text, nil
}

func replaceTextBool(params map[string]any, name string, defaultValue bool) (bool, error) {
	value, exists := params[name]
	if !exists || value == nil {
		return defaultValue, nil
	}
	if typed, ok := value.(bool); ok {
		return typed, nil
	}
	if typed, ok := value.(string); ok {
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err == nil {
			return parsed, nil
		}
	}
	return false, fmt.Errorf("ReplaceTextUnit: %s must be a boolean", name)
}

func replaceLiteralText(input, find, replacement string, caseSensitive, replaceAll bool) string {
	if caseSensitive {
		if replaceAll {
			return strings.ReplaceAll(input, find, replacement)
		}
		return strings.Replace(input, find, replacement, 1)
	}

	pattern := regexp.MustCompile("(?i)" + regexp.QuoteMeta(find))
	if replaceAll {
		return pattern.ReplaceAllStringFunc(input, func(string) string { return replacement })
	}
	match := pattern.FindStringIndex(input)
	if match == nil {
		return input
	}
	return input[:match[0]] + replacement + input[match[1]:]
}

func (u *ReplaceTextUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewReplaceTextUnit() ReplaceTextUnit {
	u := ReplaceTextUnit{}
	u.UnitName = u.GetUnitName()
	return u
}

func init() {
	unit.RegisterUnitFactory("ReplaceTextUnit", func() unit.ExecutableUnit {
		u := &ReplaceTextUnit{}
		u.UnitName = u.GetUnitName()
		return u
	})
}
