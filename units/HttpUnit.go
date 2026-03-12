package units

import (
	"context"
	"errors"
	"github.com/ninenhan/go-workflow/fn"
	xhttp "github.com/ninenhan/go-workflow/kit"
	unit "github.com/ninenhan/go-workflow/worker/unit"
	"log/slog"
	"reflect"
)

type HttpUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*HttpUnit)(nil) // ✅ 编译期检查

func (t *HttpUnit) GetUnitName() string {
	return reflect.TypeOf(HttpUnit{}).Name()
}

func (t *HttpUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	input := self.Input
	request, e := fn.ConvertByJSON[any, xhttp.XRequest](input.Data)
	if e != nil {
		slog.Error("转换失败", "err", e)
		return nil, errors.New("invalid input type")
	}
	ch := make(chan any)
	go func() {
		err := xhttp.HandlerHttpWithChannel(request, false, ch)
		if err != nil {
			slog.Error("调用失败", "err", err)
		}
	}()
	var result []any
	for message := range ch {
		//TODO 如果是sse，可以通过event push出去
		if data, ok := message.([]byte); ok {
			result = append(result, string(data))
		} else {
			result = append(result, message)
		}
	}
	return &unit.ExecutionResult{
		NodeName: t.UnitName,
		Data:     result,
		Stream:   false,
		Raw:      result,
	}, nil
}

func (t *HttpUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewHttpUnit() HttpUnit {
	unit := HttpUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit.RegisterUnitFactory("HttpUnit", func() unit.ExecutableUnit {
		unit := &HttpUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
}
