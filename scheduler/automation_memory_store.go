package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryAutomationStore provides the same scheduling contract as the durable
// store without persistence. It is suitable for tests and short-lived apps.
type MemoryAutomationStore struct {
	mu        sync.Mutex
	schedules map[string]AutomationSchedule
}

func NewMemoryAutomationStore() *MemoryAutomationStore {
	return &MemoryAutomationStore{schedules: make(map[string]AutomationSchedule)}
}

func (s *MemoryAutomationStore) ReconcileSchedules(_ context.Context, desired []AutomationSchedule) error {
	if s == nil {
		return errors.New("automation store is not configured")
	}
	seen := make(map[string]struct{}, len(desired))
	for _, schedule := range desired {
		if err := validateAutomationSchedule(schedule); err != nil {
			return err
		}
		if _, exists := seen[schedule.Key]; exists {
			return fmt.Errorf("automation schedule key is duplicated: %s", schedule.Key)
		}
		seen[schedule.Key] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.schedules == nil {
		s.schedules = make(map[string]AutomationSchedule)
	}
	for key, existing := range s.schedules {
		if _, exists := seen[key]; !exists {
			existing.Enabled = false
			existing.ClaimToken = ""
			existing.ClaimUntil = time.Time{}
			s.schedules[key] = existing
		}
	}
	for _, desiredSchedule := range desired {
		desiredSchedule = cloneAutomationSchedule(desiredSchedule)
		desiredSchedule.Enabled = true
		existing, exists := s.schedules[desiredSchedule.Key]
		if exists {
			desiredSchedule.Input = cloneAutomationInput(existing.Input)
			desiredSchedule.LastRunAt = existing.LastRunAt
			desiredSchedule.LastRunID = existing.LastRunID
			desiredSchedule.LastError = existing.LastError
			if existing.Fingerprint == desiredSchedule.Fingerprint {
				desiredSchedule.NextRunAt = existing.NextRunAt
				desiredSchedule.ClaimToken = existing.ClaimToken
				desiredSchedule.ClaimUntil = existing.ClaimUntil
			}
		}
		s.schedules[desiredSchedule.Key] = desiredSchedule
	}
	return nil
}

func (s *MemoryAutomationStore) ClaimDueSchedules(
	_ context.Context,
	now time.Time,
	limit int,
	lease time.Duration,
) ([]AutomationSchedule, error) {
	if s == nil {
		return nil, errors.New("automation store is not configured")
	}
	if limit <= 0 || limit > 100 {
		return nil, errors.New("automation claim limit must be between 1 and 100")
	}
	if lease <= 0 {
		return nil, errors.New("automation claim lease must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	eligible := make([]AutomationSchedule, 0, len(s.schedules))
	for _, schedule := range s.schedules {
		if schedule.Enabled && !schedule.NextRunAt.After(now) &&
			(schedule.ClaimUntil.IsZero() || !schedule.ClaimUntil.After(now)) {
			eligible = append(eligible, schedule)
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].NextRunAt.Equal(eligible[j].NextRunAt) {
			return eligible[i].Key < eligible[j].Key
		}
		return eligible[i].NextRunAt.Before(eligible[j].NextRunAt)
	})
	if len(eligible) > limit {
		eligible = eligible[:limit]
	}
	claimed := make([]AutomationSchedule, 0, len(eligible))
	for _, schedule := range eligible {
		token, err := automationClaimToken()
		if err != nil {
			return nil, err
		}
		schedule.ClaimToken = token
		schedule.ClaimUntil = now.Add(lease).UTC()
		s.schedules[schedule.Key] = schedule
		claimed = append(claimed, cloneAutomationSchedule(schedule))
	}
	return claimed, nil
}

func (s *MemoryAutomationStore) CompleteSchedule(
	_ context.Context,
	key, claimToken, runID string,
	scheduledAt, nextRunAt time.Time,
) error {
	if key == "" || claimToken == "" || runID == "" || scheduledAt.IsZero() || nextRunAt.IsZero() {
		return errors.New("completed automation schedule is incomplete")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	schedule, exists := s.schedules[key]
	if !exists || schedule.ClaimToken != claimToken {
		return errors.New("automation schedule claim no longer exists")
	}
	schedule.LastRunAt = scheduledAt.UTC()
	schedule.LastRunID = runID
	schedule.LastError = ""
	schedule.NextRunAt = nextRunAt.UTC()
	schedule.ClaimToken = ""
	schedule.ClaimUntil = time.Time{}
	s.schedules[key] = schedule
	return nil
}

func (s *MemoryAutomationStore) FailSchedule(
	_ context.Context,
	key, claimToken, message string,
	retryAt time.Time,
) error {
	if key == "" || claimToken == "" || strings.TrimSpace(message) == "" || retryAt.IsZero() {
		return errors.New("failed automation schedule is incomplete")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	schedule, exists := s.schedules[key]
	if !exists || schedule.ClaimToken != claimToken {
		return errors.New("automation schedule claim no longer exists")
	}
	schedule.LastError = message
	schedule.NextRunAt = retryAt.UTC()
	schedule.ClaimToken = ""
	schedule.ClaimUntil = time.Time{}
	s.schedules[key] = schedule
	return nil
}

func (s *MemoryAutomationStore) UpdateScheduleInput(_ context.Context, key string, input map[string]any) error {
	if s == nil {
		return errors.New("automation store is not configured")
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("%w: automation key is required", ErrInvalidAutomationInput)
	}
	if err := validateAutomationInput(input); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	schedule, exists := s.schedules[key]
	if !exists {
		return ErrAutomationScheduleNotFound
	}
	schedule.Input = cloneAutomationInput(input)
	s.schedules[key] = schedule
	return nil
}

func (s *MemoryAutomationStore) ListSchedules(_ context.Context) ([]AutomationSchedule, error) {
	if s == nil {
		return nil, errors.New("automation store is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]AutomationSchedule, 0, len(s.schedules))
	for _, schedule := range s.schedules {
		if schedule.Enabled {
			result = append(result, cloneAutomationSchedule(schedule))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].WorkflowName == result[j].WorkflowName {
			return result[i].TriggerID < result[j].TriggerID
		}
		return result[i].WorkflowName < result[j].WorkflowName
	})
	return result, nil
}

func validateAutomationSchedule(schedule AutomationSchedule) error {
	if schedule.Key == "" || schedule.WorkflowID == "" || schedule.VersionID == "" ||
		schedule.TriggerID == "" || schedule.Fingerprint == "" || schedule.NextRunAt.IsZero() {
		return errors.New("automation schedule is incomplete")
	}
	return nil
}

func cloneAutomationSchedule(schedule AutomationSchedule) AutomationSchedule {
	schedule.Config.Weekdays = append([]int(nil), schedule.Config.Weekdays...)
	schedule.Input = cloneAutomationInput(schedule.Input)
	return schedule
}

var _ AutomationStore = (*MemoryAutomationStore)(nil)
var _ AutomationInputStore = (*MemoryAutomationStore)(nil)
