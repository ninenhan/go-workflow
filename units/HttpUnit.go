package units

import (
	"context"
	"errors"
	core "github.com/ninenhan/go-workflow"
	"github.com/ninenhan/go-workflow/fn"
	xhttp "github.com/ninenhan/go-workflow/kit"
	"log/slog"
	"reflect"
)

type HttpUnit struct {
	core.Unit
}

var _ core.ExecutableUnit = (*HttpUnit)(nil) // ✅ 编译期检查

func (t *HttpUnit) GetUnitName() string {
	return reflect.TypeOf(HttpUnit{}).Name()
}

func (t *HttpUnit) Execute(ctx context.Context, state core.ContextMap, self *core.Node) (*core.ExecutionResult, error) {
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
	return &core.ExecutionResult{
		NodeName: t.UnitName,
		Data:     result,
		Stream:   false,
		Raw:      result,
	}, nil
}

func (t *HttpUnit) GetUnitMeta() *core.Unit {
	return &t.Unit
}

func NewHttpUnit() HttpUnit {
	unit := HttpUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	//unit := &HttpUnit{}
	//// 自动注册 HttpUnit，注意这里注册的是非指针类型
	//core.RegisterUnit(unit.GetUnitName(), unit)
}
