package units

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// CalculateUnit performs one selected arithmetic operation on two numbers.
type CalculateUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*CalculateUnit)(nil)

func (u *CalculateUnit) GetUnitName() string {
	return reflect.TypeOf(CalculateUnit{}).Name()
}

func (u *CalculateUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil {
		return nil, fmt.Errorf("CalculateUnit: missing node")
	}
	left, err := calculateNumber(self.Params["left"], "left")
	if err != nil {
		return nil, err
	}
	right, err := calculateNumber(self.Params["right"], "right")
	if err != nil {
		return nil, err
	}
	operation := strings.TrimSpace(fmt.Sprint(self.Params["operation"]))
	if operation == "" {
		operation = "add"
	}

	var result float64
	switch operation {
	case "add":
		result = left + right
	case "subtract":
		result = left - right
	case "multiply":
		result = left * right
	case "divide":
		if right == 0 {
			return nil, fmt.Errorf("CalculateUnit: cannot divide by zero")
		}
		result = left / right
	case "remainder":
		if right == 0 {
			return nil, fmt.Errorf("CalculateUnit: cannot calculate remainder with zero")
		}
		result = math.Mod(left, right)
	case "power":
		result = math.Pow(left, right)
	default:
		return nil, fmt.Errorf("CalculateUnit: unsupported operation %q", operation)
	}
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return nil, fmt.Errorf("CalculateUnit: operation %q produced a non-finite result", operation)
	}
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: result}, nil
}

func calculateNumber(value any, parameter string) (float64, error) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, fmt.Errorf("CalculateUnit: %s must be a number", parameter)
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, fmt.Errorf("CalculateUnit: %s must be a number", parameter)
		}
		number = parsed
	default:
		return 0, fmt.Errorf("CalculateUnit: %s must be a number, got %T", parameter, value)
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("CalculateUnit: %s must be a finite number", parameter)
	}
	return number, nil
}

func (u *CalculateUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewCalculateUnit() CalculateUnit {
	u := CalculateUnit{}
	u.UnitName = u.GetUnitName()
	return u
}
