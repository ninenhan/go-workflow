package wfruntime

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
)

type memoryAsyncRunLease struct {
	token string
	until time.Time
}

func (s *MemoryStore) SaveAsyncTask(_ context.Context, task *AsyncTask) error {
	if task == nil || task.DispatchID == "" || task.RunID == "" || task.NodeID == "" || task.ExternalTaskID == "" {
		return errors.New("async task is incomplete")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.asyncTasks == nil {
		s.asyncTasks = make(map[string]*AsyncTask)
	}
	if s.asyncRunLeases == nil {
		s.asyncRunLeases = make(map[string]memoryAsyncRunLease)
	}
	if s.asyncTasks[task.DispatchID] != nil {
		return nil
	}
	copy := *task
	copy.State = AsyncTaskWaiting
	copy.CreatedAt = time.Now().UTC()
	copy.UpdatedAt = copy.CreatedAt
	s.asyncTasks[task.DispatchID] = &copy
	return nil
}

func (s *MemoryStore) SubmitAsyncResult(_ context.Context, dispatchID string, result executor.ExecuteResult) (bool, error) {
	_, hash, err := asyncResultPayload(result)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.asyncTasks[dispatchID]
	if task == nil {
		return false, ErrAsyncTaskNotFound
	}
	if task.State == AsyncTaskCancelled {
		return false, ErrAsyncTaskCancelled
	}
	if task.State != AsyncTaskWaiting {
		if task.ResultHash != hash {
			return false, ErrAsyncResultConflict
		}
		return false, nil
	}
	copy := result
	task.Result = &copy
	task.ResultHash = hash
	task.State = AsyncTaskCompleted
	task.UpdatedAt = time.Now().UTC()
	return true, nil
}

func (s *MemoryStore) ClaimCompletedAsyncTasks(_ context.Context, now time.Time, limit int, lease time.Duration) ([]*AsyncTask, error) {
	if limit <= 0 || lease <= 0 {
		return nil, errors.New("async claim limit and lease must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidates := make([]*AsyncTask, 0)
	for _, task := range s.asyncTasks {
		if task.State == AsyncTaskCompleted || task.State == AsyncTaskClaimed && !task.ClaimUntil.After(now) {
			candidates = append(candidates, task)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].UpdatedAt.Before(candidates[j].UpdatedAt) })
	claimed := make([]*AsyncTask, 0, limit)
	for _, task := range candidates {
		if len(claimed) >= limit {
			break
		}
		runLease := s.asyncRunLeases[task.RunID]
		if runLease.token != "" && runLease.until.After(now) {
			continue
		}
		token, err := newAsyncClaimToken()
		if err != nil {
			return nil, err
		}
		until := now.Add(lease).UTC()
		s.asyncRunLeases[task.RunID] = memoryAsyncRunLease{token: token, until: until}
		task.State = AsyncTaskClaimed
		task.ClaimToken = token
		task.ClaimUntil = until
		copy := *task
		claimed = append(claimed, &copy)
	}
	return claimed, nil
}

func (s *MemoryStore) AcknowledgeAsyncTask(ctx context.Context, dispatchID, claimToken string) error {
	return s.finishMemoryAsyncClaim(ctx, dispatchID, claimToken, AsyncTaskDone)
}

func (s *MemoryStore) RenewAsyncTask(_ context.Context, dispatchID, claimToken string, claimUntil time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.asyncTasks[dispatchID]
	if task == nil || task.State != AsyncTaskClaimed || task.ClaimToken != claimToken {
		return false, nil
	}
	lease := s.asyncRunLeases[task.RunID]
	if lease.token != claimToken {
		return false, nil
	}
	task.ClaimUntil = claimUntil.UTC()
	lease.until = claimUntil.UTC()
	s.asyncRunLeases[task.RunID] = lease
	return true, nil
}

func (s *MemoryStore) ReleaseAsyncTask(ctx context.Context, dispatchID, claimToken string) error {
	return s.finishMemoryAsyncClaim(ctx, dispatchID, claimToken, AsyncTaskCompleted)
}

func (s *MemoryStore) finishMemoryAsyncClaim(_ context.Context, dispatchID, claimToken string, state AsyncTaskState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.asyncTasks[dispatchID]
	if task == nil || task.ClaimToken != claimToken {
		return ErrAsyncTaskNotFound
	}
	task.State = state
	task.ClaimToken = ""
	task.ClaimUntil = time.Time{}
	lease := s.asyncRunLeases[task.RunID]
	if lease.token == claimToken {
		delete(s.asyncRunLeases, task.RunID)
	}
	return nil
}

func (s *MemoryStore) CancelAsyncTasks(_ context.Context, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.asyncTasks {
		if task.RunID == runID && task.State != AsyncTaskDone {
			task.State = AsyncTaskCancelled
			task.ClaimToken = ""
			task.ClaimUntil = time.Time{}
		}
	}
	delete(s.asyncRunLeases, runID)
	return nil
}

var _ AsyncTaskStore = (*MemoryStore)(nil)
