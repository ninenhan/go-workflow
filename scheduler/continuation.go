package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/runner"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
)

const (
	continuationPollInterval = time.Second
	continuationClaimLease   = time.Minute
	continuationClaimLimit   = 32
)

func (s *Service) startContinuationLoop() {
	if s == nil {
		return
	}
	if _, ok := s.store.(wfruntime.AsyncTaskStore); !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.continuationCancel = cancel
	s.continuationDone = make(chan struct{})
	go s.runContinuationLoop(ctx)
}

func (s *Service) stopContinuationLoop(ctx context.Context) error {
	if s == nil || s.continuationCancel == nil {
		return nil
	}
	s.continuationCancel()
	select {
	case <-s.continuationDone:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown async continuation scheduler: %w", ctx.Err())
	}
}

func (s *Service) SubmitAsyncResult(ctx context.Context, dispatchID string, result executor.ExecuteResult) (bool, error) {
	store, ok := s.store.(wfruntime.AsyncTaskStore)
	if !ok {
		return false, errors.New("async task store is not configured")
	}
	accepted, err := store.SubmitAsyncResult(ctx, dispatchID, result)
	if err != nil {
		return false, err
	}
	select {
	case s.continuationWake <- struct{}{}:
	default:
	}
	return accepted, nil
}

func (s *Service) runContinuationLoop(ctx context.Context) {
	defer func() {
		s.continuationWG.Wait()
		close(s.continuationDone)
	}()
	ticker := time.NewTicker(continuationPollInterval)
	defer ticker.Stop()
	for {
		s.processCompletedAsyncTasks(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.continuationWake:
		}
	}
}

func (s *Service) processCompletedAsyncTasks(ctx context.Context) {
	store := s.store.(wfruntime.AsyncTaskStore)
	tasks, err := store.ClaimCompletedAsyncTasks(ctx, time.Now().UTC(), continuationClaimLimit, continuationClaimLease)
	if err != nil {
		return
	}
	for _, task := range tasks {
		s.continuationWG.Add(1)
		go func(task *wfruntime.AsyncTask) {
			defer s.continuationWG.Done()
			if err := s.processCompletedAsyncTask(ctx, task); err != nil {
				_ = store.ReleaseAsyncTask(context.WithoutCancel(ctx), task.DispatchID, task.ClaimToken)
			}
		}(task)
	}
}

func (s *Service) processCompletedAsyncTask(ctx context.Context, task *wfruntime.AsyncTask) error {
	store := s.store.(wfruntime.AsyncTaskStore)
	if _, active := s.activeRunCancels.Load(task.RunID); active {
		return errors.New("run is still active")
	}
	run, err := s.store.LoadRun(ctx, task.RunID)
	if err != nil {
		return err
	}
	if run.Status == wfruntime.StatusCancelled {
		return store.AcknowledgeAsyncTask(ctx, task.DispatchID, task.ClaimToken)
	}
	nodeRun := run.NodeRuns[task.NodeID]
	if nodeRun == nil || nodeRun.DispatchID != task.DispatchID {
		return errors.New("async task no longer matches its run")
	}
	version, err := s.definitions.GetVersion(ctx, run.WorkflowVersionID)
	if err != nil {
		return err
	}
	plan, err := s.engine.Compiler.Compile(version)
	if err != nil {
		return err
	}
	if nodeRun.Status == wfruntime.StatusWaiting {
		if run.Status != wfruntime.StatusWaiting {
			return errors.New("run has not released its active execution yet")
		}
		continuationScheduler, ok := s.engine.Scheduler.(*runner.DefaultScheduler)
		if !ok {
			return errors.New("configured scheduler does not support async continuation")
		}
		if err := continuationScheduler.ApplyAsyncResult(ctx, plan, run, task); err != nil {
			return err
		}
	} else if !wfruntime.IsTerminal(nodeRun.Status) {
		return errors.New("async node is not ready for continuation")
	}
	if wfruntime.IsTerminal(run.Status) {
		return store.AcknowledgeAsyncTask(ctx, task.DispatchID, task.ClaimToken)
	}
	runCtx, cancel := context.WithCancel(ctx)
	if _, loaded := s.activeRunCancels.LoadOrStore(run.ID, cancel); loaded {
		cancel()
		return errors.New("run is already active")
	}
	defer cancel()
	defer s.activeRunCancels.Delete(run.ID)
	heartbeatDone := make(chan struct{})
	go s.renewAsyncClaim(runCtx, cancel, store, task, heartbeatDone)
	_, runErr := s.engine.Scheduler.Run(runCtx, plan, run)
	close(heartbeatDone)
	if runErr != nil {
		return runErr
	}
	return store.AcknowledgeAsyncTask(ctx, task.DispatchID, task.ClaimToken)
}

func (s *Service) renewAsyncClaim(ctx context.Context, cancel context.CancelFunc, store wfruntime.AsyncTaskStore, task *wfruntime.AsyncTask, done <-chan struct{}) {
	ticker := time.NewTicker(continuationClaimLease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			renewed, err := store.RenewAsyncTask(ctx, task.DispatchID, task.ClaimToken, time.Now().Add(continuationClaimLease))
			if err != nil || !renewed {
				cancel()
				return
			}
		}
	}
}
