package units

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

type TimeoutUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*TimeoutUnit)(nil)

func (t *TimeoutUnit) GetUnitName() string {
	return reflect.TypeOf(TimeoutUnit{}).Name()
}

func (t *TimeoutUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	timeout, err := timeoutDuration(self)
	if err != nil {
		return nil, err
	}
	var input any
	if self != nil && self.Input != nil {
		input = self.Input.Data
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-timer.C:
		return &unit.ExecutionResult{NodeName: t.UnitName, Data: input}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("TimeoutUnit interrupted: %w", ctx.Err())
	}
}

func timeoutDuration(self *unit.Node) (time.Duration, error) {
	if self != nil && self.Params != nil {
		if value, ok := self.Params["duration_ms"]; ok {
			return parseTimeoutMilliseconds(value)
		}
	}
	if self != nil && self.Input != nil {
		if value, ok := self.Input.Data.(string); ok && strings.TrimSpace(value) != "" {
			return parseTimeoutMilliseconds(value)
		}
	}
	return time.Second, nil
}

func parseTimeoutMilliseconds(value any) (time.Duration, error) {
	milliseconds, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
	if err != nil || math.IsNaN(milliseconds) || math.IsInf(milliseconds, 0) || milliseconds <= 0 || milliseconds > float64(math.MaxInt64)/float64(time.Millisecond) {
		return 0, fmt.Errorf("TimeoutUnit: duration_ms must be a positive number")
	}
	return time.Duration(milliseconds * float64(time.Millisecond)), nil
}

func (t *TimeoutUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewTimeoutUnit() TimeoutUnit {
	unit := TimeoutUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit.RegisterUnitFactory("TimeoutUnit", func() unit.ExecutableUnit {
		unit := &TimeoutUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
}
