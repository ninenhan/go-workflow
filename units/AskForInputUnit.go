package units

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// AskForInputUnit returns a value collected by the workflow run form.
type AskForInputUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*AskForInputUnit)(nil)

func (u *AskForInputUnit) GetUnitName() string {
	return reflect.TypeOf(AskForInputUnit{}).Name()
}

func (u *AskForInputUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, fmt.Errorf("AskForInputUnit: answer is required")
	}
	inputType := "text"
	if configured, exists := self.Params["input_type"]; exists {
		value, ok := configured.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("AskForInputUnit: input_type must be non-empty text")
		}
		inputType = strings.TrimSpace(value)
	}

	answer, err := validateAskedInput(inputType, self.Input.Data)
	if err != nil {
		return nil, err
	}
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: answer}, nil
}

func validateAskedInput(inputType string, value any) (any, error) {
	switch inputType {
	case "text":
		answer, ok := value.(string)
		if !ok || strings.TrimSpace(answer) == "" {
			return nil, fmt.Errorf("AskForInputUnit: text answer must be non-empty text")
		}
		return answer, nil
	case "number":
		number, ok := askedInputNumber(value)
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("AskForInputUnit: number answer must be finite")
		}
		return number, nil
	case "date":
		return askedTemporalInput(value, "2006-01-02", "date")
	case "time":
		return askedTemporalInput(value, "15:04", "time")
	default:
		return nil, fmt.Errorf("AskForInputUnit: unsupported input_type %q", inputType)
	}
}

func askedInputNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func askedTemporalInput(value any, layout string, kind string) (string, error) {
	answer, ok := value.(string)
	if !ok || strings.TrimSpace(answer) == "" {
		return "", fmt.Errorf("AskForInputUnit: %s answer must be non-empty text", kind)
	}
	answer = strings.TrimSpace(answer)
	if parsed, err := time.Parse(layout, answer); err != nil || parsed.Format(layout) != answer {
		return "", fmt.Errorf("AskForInputUnit: %s answer is invalid", kind)
	}
	return answer, nil
}

func (u *AskForInputUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewAskForInputUnit() AskForInputUnit {
	u := AskForInputUnit{}
	u.UnitName = u.GetUnitName()
	return u
}

func init() {
	unit.RegisterUnitFactory("AskForInputUnit", func() unit.ExecutableUnit {
		u := &AskForInputUnit{}
		u.UnitName = u.GetUnitName()
		return u
	})
}
