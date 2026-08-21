package units

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"time"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

const maxDateAdjustment = 1_000_000

// AdjustDateUnit applies calendar-aware or duration-aware arithmetic to a date.
type AdjustDateUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*AdjustDateUnit)(nil)

func (u *AdjustDateUnit) GetUnitName() string {
	return reflect.TypeOf(AdjustDateUnit{}).Name()
}

func (u *AdjustDateUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	const action = "AdjustDateUnit"
	value, err := workflowDateInput(action, self)
	if err != nil {
		return nil, err
	}
	timezone, err := workflowDateTimezone(action, self.Params, "preserve")
	if err != nil {
		return nil, err
	}
	if timezone != "preserve" {
		location, err := workflowDateLocation(action, timezone)
		if err != nil {
			return nil, err
		}
		value = value.In(location)
	}

	amount, err := adjustDateAmount(self.Params["amount"])
	if err != nil {
		return nil, err
	}
	operation, err := workflowDateChoice(action, self.Params, "operation", "add")
	if err != nil {
		return nil, err
	}
	sign := 1
	switch operation {
	case "add":
	case "subtract":
		sign = -1
	default:
		return nil, fmt.Errorf("%s: unsupported operation %q", action, operation)
	}

	dateUnit, err := workflowDateChoice(action, self.Params, "unit", "day")
	if err != nil {
		return nil, err
	}
	signedAmount := sign * amount
	switch dateUnit {
	case "second":
		value = value.Add(time.Duration(signedAmount) * time.Second)
	case "minute":
		value = value.Add(time.Duration(signedAmount) * time.Minute)
	case "hour":
		value = value.Add(time.Duration(signedAmount) * time.Hour)
	case "day":
		value = value.AddDate(0, 0, signedAmount)
	case "week":
		value = value.AddDate(0, 0, signedAmount*7)
	case "month":
		value = addClampedCalendarMonths(value, signedAmount)
	case "year":
		value = addClampedCalendarMonths(value, signedAmount*12)
	default:
		return nil, fmt.Errorf("%s: unsupported unit %q", action, dateUnit)
	}

	result, err := workflowDateResult(action, value)
	if err != nil {
		return nil, err
	}
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: result}, nil
}

func adjustDateAmount(value any) (int, error) {
	number, err := calculateNumber(value, "amount")
	if err != nil || number < 0 || math.Trunc(number) != number || number > maxDateAdjustment {
		return 0, fmt.Errorf("AdjustDateUnit: amount must be an integer from 0 to %d", maxDateAdjustment)
	}
	return int(number), nil
}

func addClampedCalendarMonths(value time.Time, months int) time.Time {
	firstOfTargetMonth := time.Date(
		value.Year(), value.Month()+time.Month(months), 1,
		value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), value.Location(),
	)
	lastTargetDay := time.Date(
		firstOfTargetMonth.Year(), firstOfTargetMonth.Month()+1, 0,
		value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), value.Location(),
	).Day()
	day := value.Day()
	if day > lastTargetDay {
		day = lastTargetDay
	}
	return time.Date(
		firstOfTargetMonth.Year(), firstOfTargetMonth.Month(), day,
		value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), value.Location(),
	)
}

func (u *AdjustDateUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewAdjustDateUnit() AdjustDateUnit {
	u := AdjustDateUnit{}
	u.UnitName = u.GetUnitName()
	return u
}
