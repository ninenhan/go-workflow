package wfruntime

import (
	"context"
	"errors"
	"sync"
)

type MemoryStore struct {
	mu        sync.RWMutex
	runs      map[string]*WorkflowRun
	snapshots map[string][]*RunSnapshot
	events    map[string][]RunEvent
	asyncTasks map[string]*AsyncTask
	asyncRunLeases map[string]memoryAsyncRunLease
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		runs:      make(map[string]*WorkflowRun),
		snapshots: make(map[string][]*RunSnapshot),
		events:    make(map[string][]RunEvent),
		asyncTasks: make(map[string]*AsyncTask),
		asyncRunLeases: make(map[string]memoryAsyncRunLease),
	}
}

func (s *MemoryStore) SaveRun(_ context.Context, run *WorkflowRun) error {
	if run == nil {
		return errors.New("run is nil")
	}
	s.mu.Lock()
	s.runs[run.ID] = run.Clone()
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) LoadRun(_ context.Context, runID string) (*WorkflowRun, error) {
	s.mu.RLock()
	run := s.runs[runID]
	s.mu.RUnlock()
	if run == nil {
		return nil, ErrRunNotFound
	}
	return run.Clone(), nil
}

func (s *MemoryStore) ListRuns(_ context.Context) ([]*WorkflowRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*WorkflowRun, 0, len(s.runs))
	for _, run := range s.runs {
		if run == nil {
			continue
		}
		out = append(out, run.Clone())
	}
	return out, nil
}

func (s *MemoryStore) SaveSnapshot(_ context.Context, snapshot *RunSnapshot) error {
	if snapshot == nil {
		return errors.New("snapshot is nil")
	}
	s.mu.Lock()
	s.snapshots[snapshot.RunID] = append(s.snapshots[snapshot.RunID], cloneSnapshot(snapshot))
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) Snapshots(_ context.Context, runID string) ([]*RunSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.snapshots[runID]
	out := make([]*RunSnapshot, 0, len(src))
	for _, snapshot := range src {
		out = append(out, cloneSnapshot(snapshot))
	}
	return out, nil
}

func (s *MemoryStore) AppendEvent(_ context.Context, event RunEvent) error {
	s.mu.Lock()
	s.events[event.RunID] = append(s.events[event.RunID], event)
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) Events(_ context.Context, runID string) ([]RunEvent, error) {
	s.mu.RLock()
	events := append([]RunEvent{}, s.events[runID]...)
	s.mu.RUnlock()
	return events, nil
}
