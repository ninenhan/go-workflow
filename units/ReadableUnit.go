package units

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"text/template"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// ReadableUnit renders workflow data into JSON-safe text.
type ReadableUnit struct {
	unit.Unit
	Format   string `json:"format,omitempty"`
	Template string `json:"template,omitempty"`
}

var _ unit.ExecutableUnit = (*ReadableUnit)(nil)

func (t *ReadableUnit) GetUnitName() string {
	return reflect.TypeOf(ReadableUnit{}).Name()
}

func (t *ReadableUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, errors.New("ReadableUnit: missing input")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("ReadableUnit: %w", err)
	}

	format := strings.ToLower(strings.TrimSpace(t.Format))
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return nil, fmt.Errorf("ReadableUnit: unsupported format %q", t.Format)
	}
	if format == "json" && strings.TrimSpace(t.Template) != "" {
		return nil, errors.New("ReadableUnit: template is only supported for text format")
	}

	var (
		rendered string
		err      error
	)
	if strings.TrimSpace(t.Template) != "" {
		rendered, err = renderReadableTemplate(t.Template, self.Input.Data)
	} else if format == "json" {
		rendered, err = marshalReadableJSON(self.Input.Data)
	} else {
		rendered, err = renderReadableText(self.Input.Data)
	}
	if err != nil {
		return nil, err
	}
	return &unit.ExecutionResult{
		NodeName: t.UnitName,
		Data:     rendered,
	}, nil
}

func renderReadableTemplate(source string, input any) (string, error) {
	compiled, err := template.New("content").Option("missingkey=error").Parse(source)
	if err != nil {
		return "", fmt.Errorf("ReadableUnit: parse template: %w", err)
	}
	var rendered bytes.Buffer
	if err := compiled.Execute(&rendered, input); err != nil {
		return "", fmt.Errorf("ReadableUnit: render template: %w", err)
	}
	return rendered.String(), nil
}

func renderReadableText(input any) (string, error) {
	switch value := input.(type) {
	case string:
		return value, nil
	case []byte:
		return string(value), nil
	case json.RawMessage:
		if !json.Valid(value) {
			return "", errors.New("ReadableUnit: input contains invalid JSON")
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, value); err != nil {
			return "", fmt.Errorf("ReadableUnit: compact JSON: %w", err)
		}
		return compact.String(), nil
	case nil:
		return "null", nil
	case bool:
		return fmt.Sprint(value), nil
	case float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(value), nil
	default:
		return marshalReadableJSON(value)
	}
}

func marshalReadableJSON(input any) (string, error) {
	encoded, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return "", fmt.Errorf("ReadableUnit: encode JSON: %w", err)
	}
	return string(encoded), nil
}

func (t *ReadableUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewReadableUnit() ReadableUnit {
	action := ReadableUnit{Format: "text"}
	action.UnitName = action.GetUnitName()
	return action
}

func init() {
	unit.RegisterUnitFactory("ReadableUnit", func() unit.ExecutableUnit {
		action := &ReadableUnit{Format: "text"}
		action.UnitName = action.GetUnitName()
		return action
	})
}
