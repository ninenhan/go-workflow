package units

import (
	"errors"
	"github.com/ninenhan/go-workflow/flow"
	"github.com/ninenhan/go-workflow/fn"
	xhttp "github.com/ninenhan/go-workflow/kit"
	"log/slog"
	"reflect"
)

type HttpUnit struct {
	flow.BaseUnit
}

func (t *HttpUnit) GetUnitName() string {
	return reflect.TypeOf(LogUnit{}).Name()
}

func (t *HttpUnit) Execute(ctx *flow.PipelineContext, input *flow.Input) (*flow.Output, error) {
	request, e := fn.ConvertByJSON[any, xhttp.XRequest](input.Data)
	if e != nil {
		return nil, errors.New("invalid input type")
	}
	ch := make(chan any)
	go func() {
		err := xhttp.HandlerWithChannel(request, ch)
		if err != nil {
			slog.Error("调用失败", "err", err)
		}
	}()
	var result []any
	for message := range ch {
		//TODO 如果是sse，可以通过event push出去
		result = append(result, message)
	}
	o := &flow.Output{
		Data: result,
	}
	return o, nil
}

func NewHttpUnit() HttpUnit {
	unit := HttpUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit := &HttpUnit{}
	// 自动注册 HttpUnit，注意这里注册的是非指针类型
	flow.RegisterUnit(unit.GetUnitName(), unit)
}
