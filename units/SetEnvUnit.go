package units

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

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
	if self == nil {
		return nil, errors.New("SetEnvUnit: missing node")
	}
	input := any(nil)
	if self.Input != nil {
		input = self.Input.Data
	}
	name, _ := self.Params["variable_name"].(string)
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "__") {
		return nil, fmt.Errorf("SetEnvUnit: workflow variable name %q is reserved", name)
	}
	if name == "" {
		return executeSetEnvAssignments(t.UnitName, input, self.Params)
	}
	mode, _ := self.Params["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return nil, errors.New("SetEnvUnit: params.mode is required")
	}
	if mode == "delete" {
		return &unit.ExecutionResult{
			NodeName:        t.UnitName,
			Data:            input,
			DeleteVariables: []string{name},
		}, nil
	}
	if mode != "set" && mode != "merge" {
		return nil, fmt.Errorf("SetEnvUnit: unsupported mode %q", mode)
	}
	valuePath, _ := self.Params["value_path"].(string)
	value, found := lookupSetEnvValue(input, valuePath)
	if !found {
		return nil, errors.New("SetEnvUnit: value_path does not exist in input")
	}
	if mode == "merge" {
		next, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("SetEnvUnit: merge value must be an object")
		}
		merged := map[string]any{}
		if current := state[name]; current != nil && current.Data != nil {
			existing, ok := current.Data.(map[string]any)
			if !ok {
				return nil, errors.New("SetEnvUnit: existing variable must be an object for merge")
			}
			for key, item := range existing {
				merged[key] = item
			}
		}
		for key, item := range next {
			merged[key] = item
		}
		value = merged
	}
	return &unit.ExecutionResult{
		NodeName: t.UnitName,
		Data:     input,
		Variables: map[string]any{
			name: value,
		},
	}, nil
}

func executeSetEnvAssignments(nodeName string, input any, params map[string]any) (*unit.ExecutionResult, error) {
	mode, _ := params["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "set"
	}
	if mode != "set" {
		return nil, fmt.Errorf("SetEnvUnit: multi-variable assignments only support set mode, got %q", mode)
	}
	if path, _ := params["value_path"].(string); strings.TrimSpace(path) != "" {
		return nil, errors.New("SetEnvUnit: multi-variable assignments cannot use params.value_path")
	}
	assignments, ok := input.(map[string]any)
	if !ok {
		return nil, errors.New("SetEnvUnit: multi-variable assignments require object input")
	}
	if len(assignments) == 0 {
		return nil, errors.New("SetEnvUnit: multi-variable assignments require at least one value")
	}
	variables := make(map[string]any, len(assignments))
	for name, value := range assignments {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || trimmed != name {
			return nil, fmt.Errorf("SetEnvUnit: invalid workflow variable name %q", name)
		}
		if strings.HasPrefix(trimmed, "__") {
			return nil, fmt.Errorf("SetEnvUnit: workflow variable name %q is reserved", name)
		}
		variables[name] = value
	}
	return &unit.ExecutionResult{
		NodeName:  nodeName,
		Data:      input,
		Variables: variables,
	}, nil
}

func lookupSetEnvValue(value any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return value, true
	}
	current := value
	for _, segment := range strings.Split(path, ".") {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[segment]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func (t *SetEnvUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewSetEnvUnit() SetEnvUnit {
	unit := SetEnvUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}
