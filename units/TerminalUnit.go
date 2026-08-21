package units

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"text/template"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

type TerminalUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*TerminalUnit)(nil)

func (t *TerminalUnit) GetUnitName() string {
	return reflect.TypeOf(TerminalUnit{}).Name()
}

func (t *TerminalUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil {
		return nil, errors.New("TerminalUnit: missing node")
	}
	input := any(nil)
	if self.Input != nil {
		input = self.Input.Data
	}
	mode, _ := self.Params["response_mode"].(string)
	switch strings.TrimSpace(mode) {
	case "variables":
		name, _ := self.Params["output_name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, errors.New("TerminalUnit: params.output_name is required")
		}
		return &unit.ExecutionResult{
			NodeName: t.UnitName,
			Data:     map[string]any{name: input},
		}, nil
	case "text":
		source, _ := self.Params["text_template"].(string)
		if strings.TrimSpace(source) == "" {
			return nil, errors.New("TerminalUnit: params.text_template is required")
		}
		compiled, err := template.New("result").Option("missingkey=error").Parse(source)
		if err != nil {
			return nil, fmt.Errorf("TerminalUnit: parse text_template: %w", err)
		}
		var rendered bytes.Buffer
		if err := compiled.Execute(&rendered, input); err != nil {
			return nil, fmt.Errorf("TerminalUnit: render text_template: %w", err)
		}
		return &unit.ExecutionResult{NodeName: t.UnitName, Data: rendered.String()}, nil
	default:
		return nil, fmt.Errorf("TerminalUnit: unsupported response_mode %q", mode)
	}
}

func (t *TerminalUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewTerminalUnit() TerminalUnit {
	unit := TerminalUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}
