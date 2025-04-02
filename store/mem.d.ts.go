package store

import (
	"errors"
)

// -----------------------------
// Store
// -----------------------------

type PipelineState struct {
	CurrentStageIndex int
	// 如果Stage内部有多个Unit并行或串行执行的上下文，也需要记录当前Unit的进度
	CurrentUnitIndex int
	Status           string
	// 可以存储上一阶段的输出数据，用于从中间点恢复
	LastOutput any
}

type InMemoryState struct {
	data PipelineState
}

func (s *InMemoryState) Save(state PipelineState) error {
	s.data = state
	return nil
}
func (s *InMemoryState) Load() (PipelineState, error) {
	if &s.data == nil {
		return PipelineState{}, errors.New("no state")
	}
	return s.data, nil
}
