package store

import (
	"context"
	"errors"
	"sync"

	workflow "github.com/ninenhan/go-workflow"
)

type MemoryStateStore struct {
	mu   sync.RWMutex
	data map[string]*workflow.ExecutionState
}

func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{
		data: make(map[string]*workflow.ExecutionState),
	}
}

func (s *MemoryStateStore) Save(_ context.Context, state *workflow.ExecutionState) error {
	if state == nil {
		return errors.New("state is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = make(map[string]*workflow.ExecutionState)
	}
	s.data[s.key(state.WorkflowID, state.RunID)] = state.Clone()
	return nil
}

func (s *MemoryStateStore) Load(_ context.Context, workflowID, runID string) (*workflow.ExecutionState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data == nil {
		return nil, errors.New("state not found")
	}
	state := s.data[s.key(workflowID, runID)]
	if state == nil {
		return nil, errors.New("state not found")
	}
	return state.Clone(), nil
}

func (s *MemoryStateStore) key(workflowID, runID string) string {
	return workflowID + "::" + runID
}
