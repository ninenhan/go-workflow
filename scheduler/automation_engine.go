package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
)

const (
	defaultAutomationPollInterval = time.Second
	automationSyncInterval        = 30 * time.Second
	automationClaimLease          = time.Minute
	automationFailureRetry        = time.Minute
	automationClaimLimit          = 32
)

type AutomationStatus struct {
	AutomationSchedule
	LastRunStatus wfruntime.Status `json:"last_run_status,omitempty"`
}

func (s *Service) StartAutomations(ctx context.Context, pollInterval time.Duration) error {
	if s == nil || s.automations == nil {
		return errors.New("automation store is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if pollInterval == 0 {
		pollInterval = defaultAutomationPollInterval
	}
	if pollInterval < 100*time.Millisecond || pollInterval > time.Minute {
		return errors.New("automation poll interval must be between 100ms and 1m")
	}
	if err := s.SyncAutomations(ctx, time.Now()); err != nil {
		return err
	}
	if err := s.RunDueAutomations(ctx, time.Now()); err != nil {
		return err
	}

	s.automationMu.Lock()
	defer s.automationMu.Unlock()
	if s.automationCancel != nil {
		return errors.New("automation scheduler is already running")
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	s.automationCancel = cancel
	s.automationDone = done
	s.automationError = ""
	go s.runAutomationLoop(loopCtx, done, pollInterval)
	return nil
}

func (s *Service) SyncAutomations(ctx context.Context, now time.Time) error {
	if s == nil || s.automations == nil || s.definitions == nil {
		return errors.New("automation scheduler is not configured")
	}
	workflows, err := s.definitions.ListWorkflows(ctx)
	if err != nil {
		return err
	}
	desired := make([]AutomationSchedule, 0)
	for _, workflow := range workflows {
		if workflow == nil || workflow.ActiveVersion == "" {
			continue
		}
		version, err := s.definitions.GetActiveVersion(ctx, workflow.ID)
		if err != nil {
			return fmt.Errorf("load active automation workflow %s: %w", workflow.ID, err)
		}
		if version.Definition == nil {
			return fmt.Errorf("active automation workflow %s has no definition", workflow.ID)
		}
		if err := validateAutomationTriggers(version.Definition.Triggers); err != nil {
			return fmt.Errorf("validate automation workflow %s: %w", workflow.ID, err)
		}
		for _, trigger := range version.Definition.Triggers {
			if trigger.Type != definition.TriggerCron || !trigger.Enabled {
				continue
			}
			config, err := ParseAutomationSchedule(trigger)
			if err != nil {
				return err
			}
			nextRunAt, err := NextAutomationOccurrence(config, now)
			if err != nil {
				return fmt.Errorf("calculate automation trigger %s: %w", trigger.ID, err)
			}
			fingerprint, err := automationScheduleFingerprint(version.ID, trigger, config)
			if err != nil {
				return err
			}
			name := version.Definition.Name
			if name == "" {
				name = workflow.Name
			}
			desired = append(desired, AutomationSchedule{
				Key:          automationScheduleKey(workflow.ID, trigger.ID),
				WorkflowID:   workflow.ID,
				WorkflowName: name,
				VersionID:    version.ID,
				TriggerID:    trigger.ID,
				Fingerprint:  fingerprint,
				Config:       config,
				Enabled:      true,
				NextRunAt:    nextRunAt,
			})
		}
	}
	sort.Slice(desired, func(i, j int) bool { return desired[i].Key < desired[j].Key })
	return s.automations.ReconcileSchedules(ctx, desired)
}

func (s *Service) RunDueAutomations(ctx context.Context, now time.Time) error {
	if s == nil || s.automations == nil {
		return errors.New("automation store is not configured")
	}
	claimed, err := s.automations.ClaimDueSchedules(
		ctx,
		now.UTC(),
		automationClaimLimit,
		automationClaimLease,
	)
	if err != nil {
		return err
	}
	var failures []error
	for _, schedule := range claimed {
		if err := s.startClaimedAutomation(ctx, schedule, now); err != nil {
			failures = append(failures, err)
			retryErr := s.automations.FailSchedule(
				ctx,
				schedule.Key,
				schedule.ClaimToken,
				err.Error(),
				now.Add(automationFailureRetry),
			)
			if retryErr != nil {
				failures = append(failures, retryErr)
			}
		}
	}
	return errors.Join(failures...)
}

func (s *Service) ListAutomations(ctx context.Context) ([]AutomationStatus, error) {
	if s == nil || s.automations == nil {
		return nil, errors.New("automation store is not configured")
	}
	schedules, err := s.automations.ListSchedules(ctx)
	if err != nil {
		return nil, err
	}
	statuses := make([]AutomationStatus, 0, len(schedules))
	for _, schedule := range schedules {
		status := AutomationStatus{AutomationSchedule: schedule}
		if schedule.LastRunID != "" {
			run, loadErr := s.store.LoadRun(ctx, schedule.LastRunID)
			if loadErr == nil {
				status.LastRunStatus = run.Status
			} else if !errors.Is(loadErr, wfruntime.ErrRunNotFound) {
				return nil, loadErr
			}
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (s *Service) SetAutomationInput(ctx context.Context, key string, input map[string]any) error {
	if s == nil || s.automations == nil {
		return errors.New("automation store is not configured")
	}
	store, ok := s.automations.(AutomationInputStore)
	if !ok {
		return ErrAutomationInputNotSupported
	}
	return store.UpdateScheduleInput(ctx, key, input)
}

func (s *Service) AutomationError() string {
	if s == nil {
		return ""
	}
	s.automationMu.RLock()
	defer s.automationMu.RUnlock()
	return s.automationError
}

func (s *Service) startClaimedAutomation(
	ctx context.Context,
	schedule AutomationSchedule,
	now time.Time,
) error {
	nextRunAt, err := NextAutomationOccurrence(schedule.Config, now)
	if err != nil {
		return err
	}
	version, err := s.definitions.GetVersion(ctx, schedule.VersionID)
	if err != nil {
		return err
	}
	runID := automationRunID(schedule.Key, schedule.NextRunAt)
	if _, err := s.store.LoadRun(ctx, runID); err != nil {
		if !errors.Is(err, wfruntime.ErrRunNotFound) {
			return err
		}
		variables := cloneAutomationInput(schedule.Input)
		variables["_automation"] = map[string]any{
			"trigger_id":   schedule.TriggerID,
			"scheduled_at": schedule.NextRunAt.UTC().Format(time.RFC3339Nano),
		}
		run := &wfruntime.WorkflowRun{
			ID:                runID,
			WorkflowID:        schedule.WorkflowID,
			WorkflowVersionID: schedule.VersionID,
			CredentialScope:   s.credentialScope,
			Context: wfruntime.RunContext{
				Variables:   variables,
				NodeResults: map[string]any{},
			},
		}
		if _, err := s.StartVersion(ctx, version, run); err != nil {
			return err
		}
	}
	return s.automations.CompleteSchedule(
		ctx,
		schedule.Key,
		schedule.ClaimToken,
		runID,
		schedule.NextRunAt,
		nextRunAt,
	)
}

func (s *Service) runAutomationLoop(
	ctx context.Context,
	done chan<- struct{},
	pollInterval time.Duration,
) {
	defer close(done)
	pollTicker := time.NewTicker(pollInterval)
	syncTicker := time.NewTicker(automationSyncInterval)
	defer pollTicker.Stop()
	defer syncTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pollTicker.C:
			s.recordAutomationError(s.RunDueAutomations(ctx, time.Now()))
		case <-syncTicker.C:
			s.recordAutomationError(s.SyncAutomations(ctx, time.Now()))
		case <-s.automationWake:
			s.recordAutomationError(s.SyncAutomations(ctx, time.Now()))
		}
	}
}

func (s *Service) notifyAutomationSync() {
	if s == nil || s.automations == nil {
		return
	}
	select {
	case s.automationWake <- struct{}{}:
	default:
	}
}

func (s *Service) recordAutomationError(err error) {
	s.automationMu.Lock()
	defer s.automationMu.Unlock()
	if err == nil {
		s.automationError = ""
		return
	}
	s.automationError = err.Error()
}

func (s *Service) stopAutomations(ctx context.Context) error {
	s.automationMu.Lock()
	cancel := s.automationCancel
	done := s.automationDone
	s.automationCancel = nil
	s.automationDone = nil
	s.automationMu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown automation scheduler: %w", ctx.Err())
	}
}
