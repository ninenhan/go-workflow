package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ninenhan/go-workflow/internal/gormdb"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AutomationSchedule struct {
	Key          string                   `json:"key"`
	WorkflowID   string                   `json:"workflow_id"`
	WorkflowName string                   `json:"workflow_name"`
	VersionID    string                   `json:"version_id"`
	TriggerID    string                   `json:"trigger_id"`
	Fingerprint  string                   `json:"-"`
	Config       AutomationScheduleConfig `json:"schedule"`
	Input        map[string]any           `json:"input"`
	Enabled      bool                     `json:"enabled"`
	NextRunAt    time.Time                `json:"next_run_at"`
	LastRunAt    time.Time                `json:"last_run_at,omitempty"`
	LastRunID    string                   `json:"last_run_id,omitempty"`
	LastError    string                   `json:"last_error,omitempty"`
	ClaimToken   string                   `json:"-"`
	ClaimUntil   time.Time                `json:"-"`
}

type AutomationStore interface {
	ReconcileSchedules(ctx context.Context, desired []AutomationSchedule) error
	ClaimDueSchedules(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]AutomationSchedule, error)
	CompleteSchedule(ctx context.Context, key, claimToken, runID string, scheduledAt, nextRunAt time.Time) error
	FailSchedule(ctx context.Context, key, claimToken, message string, retryAt time.Time) error
	ListSchedules(ctx context.Context) ([]AutomationSchedule, error)
}

type AutomationInputStore interface {
	UpdateScheduleInput(ctx context.Context, key string, input map[string]any) error
}

var (
	ErrAutomationScheduleNotFound   = errors.New("automation schedule not found")
	ErrAutomationInputNotSupported  = errors.New("automation input is not supported by the configured store")
	ErrInvalidAutomationInput       = errors.New("automation input is invalid")
)

type GormAutomationStore struct {
	db        *gorm.DB
	tableName string
}

type automationScheduleRecord struct {
	Key          string         `gorm:"primaryKey;type:varchar(32)"`
	WorkflowID   string         `gorm:"index;type:varchar(128);not null"`
	WorkflowName string         `gorm:"type:varchar(255);not null"`
	VersionID    string         `gorm:"index;type:varchar(160);not null"`
	TriggerID    string         `gorm:"type:varchar(128);not null"`
	Fingerprint  string         `gorm:"type:varchar(64);not null"`
	Config       datatypes.JSON `gorm:"type:json;not null"`
	Input        datatypes.JSON `gorm:"type:json"`
	Enabled      bool           `gorm:"index;not null"`
	NextRunAt    time.Time      `gorm:"index;not null"`
	LastRunAt    *time.Time     `gorm:"index"`
	LastRunID    string         `gorm:"type:varchar(64)"`
	LastError    string         `gorm:"type:text"`
	ClaimToken   string         `gorm:"index;type:varchar(64)"`
	ClaimUntil   *time.Time     `gorm:"index"`
	CreatedAt    time.Time      `gorm:"not null"`
	UpdatedAt    time.Time      `gorm:"index;not null"`
}

func (automationScheduleRecord) TableName() string { return "workflow_automation_schedules" }

// NewGormAutomationStore constructs the legacy standalone automation adapter.
//
// Deprecated: new applications should compose the complete adapter through
// persist/gormstore.New. This constructor remains available for compatibility.
func NewGormAutomationStore(db *gorm.DB) (*GormAutomationStore, error) {
	return NewGormAutomationStoreWithTablePrefix(db, "")
}

func NewGormAutomationStoreWithTablePrefix(db *gorm.DB, tablePrefix string) (*GormAutomationStore, error) {
	if db == nil {
		return nil, errors.New("gorm db is nil")
	}
	if err := gormdb.ValidateTablePrefix(tablePrefix); err != nil {
		return nil, fmt.Errorf("configure automation store: %w", err)
	}
	tableName := gormdb.TableName(tablePrefix, automationScheduleRecord{}.TableName())
	if err := db.Table(tableName).AutoMigrate(&automationScheduleRecord{}); err != nil {
		return nil, fmt.Errorf("auto migrate automation store: %w", err)
	}
	return &GormAutomationStore{db: db, tableName: tableName}, nil
}

func (s *GormAutomationStore) ReconcileSchedules(ctx context.Context, desired []AutomationSchedule) error {
	if s == nil || s.db == nil {
		return errors.New("automation store is not configured")
	}
	keys := make([]string, 0, len(desired))
	seen := make(map[string]struct{}, len(desired))
	for _, schedule := range desired {
		if schedule.Key == "" || schedule.WorkflowID == "" || schedule.VersionID == "" ||
			schedule.TriggerID == "" || schedule.Fingerprint == "" || schedule.NextRunAt.IsZero() {
			return errors.New("automation schedule is incomplete")
		}
		if _, exists := seen[schedule.Key]; exists {
			return fmt.Errorf("automation schedule key is duplicated: %s", schedule.Key)
		}
		seen[schedule.Key] = struct{}{}
		keys = append(keys, schedule.Key)
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		for _, schedule := range desired {
			config, err := json.Marshal(schedule.Config)
			if err != nil {
				return fmt.Errorf("marshal automation schedule: %w", err)
			}
			var existing automationScheduleRecord
			queryErr := tx.Table(s.tableName).
				Where(map[string]any{"key": schedule.Key}).
				First(&existing).Error
			switch {
			case errors.Is(queryErr, gorm.ErrRecordNotFound):
				if err := tx.Table(s.tableName).Create(&automationScheduleRecord{
					Key:          schedule.Key,
					WorkflowID:   schedule.WorkflowID,
					WorkflowName: schedule.WorkflowName,
					VersionID:    schedule.VersionID,
					TriggerID:    schedule.TriggerID,
					Fingerprint:  schedule.Fingerprint,
					Config:       config,
					Input:        datatypes.JSON([]byte("{}")),
					Enabled:      true,
					NextRunAt:    schedule.NextRunAt.UTC(),
					CreatedAt:    now,
					UpdatedAt:    now,
				}).Error; err != nil {
					return err
				}
			case queryErr != nil:
				return queryErr
			default:
				updates := map[string]any{
					"workflow_id":   schedule.WorkflowID,
					"workflow_name": schedule.WorkflowName,
					"version_id":    schedule.VersionID,
					"trigger_id":    schedule.TriggerID,
					"fingerprint":   schedule.Fingerprint,
					"config":        config,
					"enabled":       true,
					"updated_at":    now,
				}
				if existing.Fingerprint != schedule.Fingerprint || existing.NextRunAt.IsZero() {
					updates["next_run_at"] = schedule.NextRunAt.UTC()
					updates["claim_token"] = ""
					updates["claim_until"] = nil
					updates["last_error"] = ""
				}
				if err := tx.Table(s.tableName).Model(&automationScheduleRecord{}).
					Where(map[string]any{"key": schedule.Key}).
					Updates(updates).Error; err != nil {
					return err
				}
			}
		}

		stale := tx.Table(s.tableName).Model(&automationScheduleRecord{}).Where("enabled = ?", true)
		if len(keys) > 0 {
			stale = stale.Not(map[string]any{"key": keys})
		}
		return stale.Updates(map[string]any{
			"enabled":     false,
			"claim_token": "",
			"claim_until": nil,
			"updated_at":  now,
		}).Error
	})
}

func (s *GormAutomationStore) ClaimDueSchedules(
	ctx context.Context,
	now time.Time,
	limit int,
	lease time.Duration,
) ([]AutomationSchedule, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("automation store is not configured")
	}
	if limit <= 0 || limit > 100 {
		return nil, errors.New("automation claim limit must be between 1 and 100")
	}
	if lease <= 0 {
		return nil, errors.New("automation claim lease must be positive")
	}
	var claimed []AutomationSchedule
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var records []automationScheduleRecord
		if err := tx.Table(s.tableName).
			Where("enabled = ? AND next_run_at <= ? AND (claim_until IS NULL OR claim_until <= ?)", true, now, now).
			Order(clause.OrderByColumn{Column: clause.Column{Name: "next_run_at"}}).
			Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).
			Limit(limit).
			Find(&records).Error; err != nil {
			return err
		}
		for _, record := range records {
			token, err := automationClaimToken()
			if err != nil {
				return err
			}
			claimUntil := now.Add(lease).UTC()
			result := tx.Table(s.tableName).Model(&automationScheduleRecord{}).
				Where(map[string]any{
					"key":         record.Key,
					"enabled":     true,
					"next_run_at": record.NextRunAt,
				}).
				Where("claim_until IS NULL OR claim_until <= ?", now).
				Updates(map[string]any{
					"claim_token": token,
					"claim_until": claimUntil,
					"updated_at":  now.UTC(),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				continue
			}
			record.ClaimToken = token
			record.ClaimUntil = &claimUntil
			schedule, err := unmarshalAutomationSchedule(record)
			if err != nil {
				return err
			}
			claimed = append(claimed, schedule)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claim due automation schedules: %w", err)
	}
	return claimed, nil
}

func (s *GormAutomationStore) CompleteSchedule(
	ctx context.Context,
	key, claimToken, runID string,
	scheduledAt, nextRunAt time.Time,
) error {
	if key == "" || claimToken == "" || runID == "" || scheduledAt.IsZero() || nextRunAt.IsZero() {
		return errors.New("completed automation schedule is incomplete")
	}
	result := s.db.WithContext(ctx).Table(s.tableName).Model(&automationScheduleRecord{}).
		Where(map[string]any{"key": key, "claim_token": claimToken}).
		Updates(map[string]any{
			"last_run_at": scheduledAt.UTC(),
			"last_run_id": runID,
			"last_error":  "",
			"next_run_at": nextRunAt.UTC(),
			"claim_token": "",
			"claim_until": nil,
			"updated_at":  time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("automation schedule claim no longer exists")
	}
	return nil
}

func (s *GormAutomationStore) FailSchedule(
	ctx context.Context,
	key, claimToken, message string,
	retryAt time.Time,
) error {
	if key == "" || claimToken == "" || strings.TrimSpace(message) == "" || retryAt.IsZero() {
		return errors.New("failed automation schedule is incomplete")
	}
	result := s.db.WithContext(ctx).Table(s.tableName).Model(&automationScheduleRecord{}).
		Where(map[string]any{"key": key, "claim_token": claimToken}).
		Updates(map[string]any{
			"last_error":  message,
			"next_run_at": retryAt.UTC(),
			"claim_token": "",
			"claim_until": nil,
			"updated_at":  time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("automation schedule claim no longer exists")
	}
	return nil
}

func (s *GormAutomationStore) UpdateScheduleInput(ctx context.Context, key string, input map[string]any) error {
	if s == nil || s.db == nil {
		return errors.New("automation store is not configured")
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("%w: automation key is required", ErrInvalidAutomationInput)
	}
	if err := validateAutomationInput(input); err != nil {
		return err
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAutomationInput, err)
	}
	result := s.db.WithContext(ctx).Table(s.tableName).Model(&automationScheduleRecord{}).
		Where(map[string]any{"key": key}).
		Update("input", datatypes.JSON(encoded))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAutomationScheduleNotFound
	}
	return nil
}

func (s *GormAutomationStore) ListSchedules(ctx context.Context) ([]AutomationSchedule, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("automation store is not configured")
	}
	var records []automationScheduleRecord
	if err := s.db.WithContext(ctx).Table(s.tableName).
		Where("enabled = ?", true).
		Order("workflow_name asc, trigger_id asc").
		Find(&records).Error; err != nil {
		return nil, err
	}
	schedules := make([]AutomationSchedule, 0, len(records))
	for _, record := range records {
		schedule, err := unmarshalAutomationSchedule(record)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, nil
}

func unmarshalAutomationSchedule(record automationScheduleRecord) (AutomationSchedule, error) {
	var config AutomationScheduleConfig
	if err := json.Unmarshal(record.Config, &config); err != nil {
		return AutomationSchedule{}, fmt.Errorf("unmarshal automation schedule: %w", err)
	}
	input := make(map[string]any)
	if len(record.Input) > 0 {
		if err := json.Unmarshal(record.Input, &input); err != nil {
			return AutomationSchedule{}, fmt.Errorf("unmarshal automation input: %w", err)
		}
		if input == nil {
			input = make(map[string]any)
		}
	}
	schedule := AutomationSchedule{
		Key:          record.Key,
		WorkflowID:   record.WorkflowID,
		WorkflowName: record.WorkflowName,
		VersionID:    record.VersionID,
		TriggerID:    record.TriggerID,
		Fingerprint:  record.Fingerprint,
		Config:       config,
		Input:        input,
		Enabled:      record.Enabled,
		NextRunAt:    record.NextRunAt,
		LastRunID:    record.LastRunID,
		LastError:    record.LastError,
		ClaimToken:   record.ClaimToken,
	}
	if record.LastRunAt != nil {
		schedule.LastRunAt = *record.LastRunAt
	}
	if record.ClaimUntil != nil {
		schedule.ClaimUntil = *record.ClaimUntil
	}
	return schedule, nil
}

func validateAutomationInput(input map[string]any) error {
	if input == nil {
		return fmt.Errorf("%w: input must be a JSON object", ErrInvalidAutomationInput)
	}
	if _, exists := input["_automation"]; exists {
		return fmt.Errorf("%w: _automation is reserved", ErrInvalidAutomationInput)
	}
	if _, err := json.Marshal(input); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAutomationInput, err)
	}
	return nil
}

func cloneAutomationInput(input map[string]any) map[string]any {
	if input == nil {
		return make(map[string]any)
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneAutomationInputValue(value)
	}
	return cloned
}

func cloneAutomationInputValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAutomationInput(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneAutomationInputValue(item)
		}
		return cloned
	default:
		return value
	}
}

func automationClaimToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate automation claim: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

var _ AutomationStore = (*GormAutomationStore)(nil)
var _ AutomationInputStore = (*GormAutomationStore)(nil)
