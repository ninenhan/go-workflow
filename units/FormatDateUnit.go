package units

import (
	"context"
	"fmt"
	"reflect"
	"strconv"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// FormatDateUnit renders a date using one selected no-code preset.
type FormatDateUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*FormatDateUnit)(nil)

func (u *FormatDateUnit) GetUnitName() string {
	return reflect.TypeOf(FormatDateUnit{}).Name()
}

func (u *FormatDateUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	const action = "FormatDateUnit"
	value, err := workflowDateInput(action, self)
	if err != nil {
		return nil, err
	}
	format, err := workflowDateChoice(action, self.Params, "format", "date_time")
	if err != nil {
		return nil, err
	}

	var result string
	switch format {
	case "iso":
		result, err = workflowDateResult(action, value)
	case "date":
		result = value.Format("2006-01-02")
	case "time":
		result = value.Format("15:04:05")
	case "date_time":
		result = value.Format("2006-01-02 15:04")
	case "unix":
		result = strconv.FormatInt(value.Unix(), 10)
	default:
		return nil, fmt.Errorf("%s: unsupported format %q", action, format)
	}
	if err != nil {
		return nil, err
	}
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: result}, nil
}

func (u *FormatDateUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewFormatDateUnit() FormatDateUnit {
	u := FormatDateUnit{}
	u.UnitName = u.GetUnitName()
	return u
}

func init() {
	unit.RegisterUnitFactory("FormatDateUnit", func() unit.ExecutableUnit {
		u := &FormatDateUnit{}
		u.UnitName = u.GetUnitName()
		return u
	})
}
