package units

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"time"

	core "github.com/ninenhan/go-workflow"
)

type TimeoutUnit struct {
	core.Unit
}

var _ core.ExecutableUnit = (*TimeoutUnit)(nil)

func (t *TimeoutUnit) GetUnitName() string {
	return reflect.TypeOf(TimeoutUnit{}).Name()
}

func (t *TimeoutUnit) Execute(ctx context.Context, state core.ContextMap, self *core.Node) (*core.ExecutionResult, error) {
	timeout := 1 * time.Second
	if self != nil && self.Input != nil {
		if val, ok := self.Input.Data.(string); ok {
			if tms, err := strconv.ParseInt(val, 10, 64); err == nil {
				timeout = time.Duration(tms) * time.Millisecond
			}
		}
	}
	select {
	case <-time.After(timeout):
		return &core.ExecutionResult{NodeName: t.UnitName}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("TimeoutUnit interrupted: %w", ctx.Err())
	}
}

func (t *TimeoutUnit) GetUnitMeta() *core.Unit {
	return &t.Unit
}

func NewTimeoutUnit() TimeoutUnit {
	unit := TimeoutUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	core.RegisterUnitFactory("TimeoutUnit", func() core.ExecutableUnit {
		unit := &TimeoutUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
}
