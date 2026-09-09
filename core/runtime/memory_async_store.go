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
		if task.ResultHash == hash {
			return false, nil
		}
	}
	if !task.ExpireAt.IsZero() && !task.ExpireAt.After(time.Now().UTC()) {
		return false, ErrAsyncTaskExpired
	}
	if task.State != AsyncTaskWaiting {
		return false, ErrAsyncResultConflict
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
	timeoutResult := executor.ExecuteResult{Status: executor.StatusFailed, Error: ErrAsyncTaskExpired.Error()}
	_, timeoutHash, err := asyncResultPayload(timeoutResult)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidates := make([]*AsyncTask, 0)
	for _, task := range s.asyncTasks {
		if task.State == AsyncTaskWaiting && !task.ExpireAt.IsZero() && !task.ExpireAt.After(now) {
			result := timeoutResult
			task.Result = &result
			task.ResultHash = timeoutHash
			task.State = AsyncTaskCompleted
			task.UpdatedAt = now.UTC()
		}
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
	now := time.Now().UTC()
	task := s.asyncTasks[dispatchID]
	if claimToken == "" || !claimUntil.After(now) || task == nil || task.State != AsyncTaskClaimed || task.ClaimToken != claimToken || !task.ClaimUntil.After(now) {
		return false, nil
	}
	lease := s.asyncRunLeases[task.RunID]
	if lease.token != claimToken || !lease.until.After(now) {
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

func (s *MemoryStore) CommitAsyncResult(ctx context.Context, task *AsyncTask, run *WorkflowRun, expectedUpdatedAt time.Time, snapshots []*RunSnapshot, events []RunEvent) error {
	if task == nil || run == nil || task.Result == nil || task.ClaimToken == "" || task.RunID != run.ID {
		return errors.New("async result commit is incomplete")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now().UTC()
	stored := s.asyncTasks[task.DispatchID]
	if stored != nil && stored.State == AsyncTaskCancelled {
		return ErrAsyncTaskCancelled
	}
	lease := s.asyncRunLeases[task.RunID]
	if stored == nil || stored.State != AsyncTaskClaimed || stored.ClaimToken != task.ClaimToken ||
		!stored.ClaimUntil.After(now) || lease.token != task.ClaimToken || !lease.until.After(now) {
		return ErrAsyncClaimLost
	}
	if stored.ResultApplied || stored.ResultHash != task.ResultHash {
		return ErrAsyncResultConflict
	}
	if stored.RunID != task.RunID || stored.NodeID != task.NodeID {
		return ErrAsyncRunChanged
	}
	if err := validateAsyncCommitRun(s.runs[run.ID], task, expectedUpdatedAt); err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		if snapshot == nil || snapshot.RunID != run.ID {
			return errors.New("async result snapshot does not match its run")
		}
	}
	for _, event := range events {
		if event.RunID != run.ID {
			return errors.New("async result event does not match its run")
		}
	}
	s.runs[run.ID] = run.Clone()
	for _, snapshot := range snapshots {
		s.snapshots[run.ID] = append(s.snapshots[run.ID], cloneSnapshot(snapshot))
	}
	s.events[run.ID] = append(s.events[run.ID], events...)
	stored.ResultApplied = true
	stored.UpdatedAt = now
	return nil
}

var _ AsyncResultStore = (*MemoryStore)(nil)
