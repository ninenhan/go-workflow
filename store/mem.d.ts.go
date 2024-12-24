package store

import (
	"errors"
	"github.com/ninenhan/go-workflow/core"
)

// -----------------------------
// Store
// -----------------------------

type PipelineState = core.PipelineState

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
