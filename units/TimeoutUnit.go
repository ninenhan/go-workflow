package units

import (
	"fmt"
	"github.com/ninenhan/go-workflow/flow"
	"reflect"
	"strconv"
	"time"
)

type TimeoutUnit struct {
	flow.BaseUnit
}

func (t *TimeoutUnit) GetUnitName() string {
	return reflect.TypeOf(TimeoutUnit{}).Name()
}

func (t *TimeoutUnit) Execute(ctx *flow.PipelineContext, i *flow.Input) (*flow.Output, error) {
	val, ok := i.Data.(string)
	timeout := 1 * time.Second
	if ok {
		// parseInt val
		t, err := strconv.ParseInt(val, 10, 64)
		if err == nil {
			timeout = time.Duration(t) * time.Millisecond
		}
	}
	select {
	case <-time.After(timeout):
		return nil, nil
	case <-ctx.Context.Done():
		return nil, fmt.Errorf("TimeoutUnit 被中断: %w", ctx.Context.Err())
	}
}

func NewTimeoutUnit() TimeoutUnit {
	unit := TimeoutUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit := &TimeoutUnit{}
	// 自动注册 HttpUnit，注意这里注册的是非指针类型
	flow.RegisterUnit(unit.GetUnitName(), unit)
}
