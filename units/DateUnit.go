package units

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

var dateUnitNow = time.Now

// DateUnit returns the current time or one explicitly selected wall-clock time.
type DateUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*DateUnit)(nil)

func (u *DateUnit) GetUnitName() string {
	return reflect.TypeOf(DateUnit{}).Name()
}

func (u *DateUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil {
		return nil, fmt.Errorf("DateUnit: missing node")
	}
	mode := "current"
	if configured, exists := self.Params["mode"]; exists {
		mode = strings.TrimSpace(fmt.Sprint(configured))
	}
	location, err := dateUnitLocation(self.Params)
	if err != nil {
		return nil, err
	}

	var value time.Time
	switch mode {
	case "current":
		value = dateUnitNow().In(location)
	case "specified":
		date, err := dateUnitParameter(self.Params, "date")
		if err != nil {
			return nil, err
		}
		clock, err := dateUnitParameter(self.Params, "time")
		if err != nil {
			return nil, err
		}
		value, err = time.ParseInLocation("2006-01-02 15:04", date+" "+clock, location)
		if err != nil {
			return nil, fmt.Errorf("DateUnit: date and time are invalid: %w", err)
		}
	default:
		return nil, fmt.Errorf("DateUnit: unsupported mode %q", mode)
	}
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: value.Format(time.RFC3339)}, nil
}

func dateUnitLocation(params map[string]any) (*time.Location, error) {
	timezone, err := workflowDateTimezone("DateUnit", params, "UTC")
	if err != nil {
		return nil, err
	}
	return workflowDateLocation("DateUnit", timezone)
}

func dateUnitParameter(params map[string]any, name string) (string, error) {
	value, exists := params[name]
	if !exists {
		return "", fmt.Errorf("DateUnit: %s is required for specified mode", name)
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("DateUnit: %s must be non-empty text", name)
	}
	return strings.TrimSpace(text), nil
}

func (u *DateUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewDateUnit() DateUnit {
	u := DateUnit{}
	u.UnitName = u.GetUnitName()
	return u
}
