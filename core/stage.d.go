package core

import (
	"context"
	"fmt"
)

// -----------------------------
// Stage与Pipeline
// -----------------------------

// Stage表示一个阶段，由多个Unit构成
// 输入数据会给到每个Unit，Unit返回结果。
// 为了简单起见，这里我们假设Stage内的Units并行执行后结果汇总为一个[]any输出

type IStage[T any] interface {
	Name() string        // 阶段名称
	Status() StageStatus // 阶段状态
	DependsOn() []string // 依赖的阶段NAME（空表示第一个阶段）
	units() []Unit       // 绑定的单元
}

type Stage struct {
	Name      string      `json:"name"`       // 阶段名称
	DependsOn string      `json:"depends_on"` // 依赖的阶段NAME（空表示第一个阶段）
	units     []Unit      // 有序或无序的Unit列表
	Status    StageStatus `json:"status"` // 阶段状态
}

func NewStage(name string, units []Unit) *Stage {
	return &Stage{Name: name, units: units}
}

func (s *Stage) Run(ctx context.Context, input any) (any, error) {
	// 对每个unit执行
	// 简单起见，依次执行（也可以用goroutine并行执行）
	// TODO 考虑是否要支持并发执行,还要考虑Deps排序
	var outputs []any
	for _, u := range s.units {
		//ctx.Value("unit") = u
		out, err := u.Run(ctx, input, &u)
		if err != nil {
			return nil, fmt.Errorf("stage %s unit %s error: %v", s.Name, u.ID(), err)
		}
		re := &UnitOutput{
			ID:     u.ID(),
			Output: out,
		}
		outputs = append(outputs, &re)
	}
	return outputs, nil
}
