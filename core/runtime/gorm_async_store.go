package wfruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type asyncTaskRecord struct {
	DispatchID     string         `gorm:"primaryKey;type:varchar(64)"`
	RunID          string         `gorm:"index;type:varchar(64);not null"`
	NodeID         string         `gorm:"type:varchar(128);not null"`
	ExternalTaskID string         `gorm:"type:varchar(255);not null"`
	Task           datatypes.JSON `gorm:"type:json;not null"`
	Result         datatypes.JSON `gorm:"type:json"`
	ResultHash     string         `gorm:"type:varchar(64)"`
	State          string         `gorm:"index;type:varchar(32);not null"`
	ClaimToken     string         `gorm:"index;type:varchar(64)"`
	ClaimUntil     *time.Time     `gorm:"index"`
	CreatedAt      time.Time      `gorm:"index;not null"`
	UpdatedAt      time.Time      `gorm:"index;not null"`
}

func (asyncTaskRecord) TableName() string { return "workflow_async_tasks" }

type asyncRunLeaseRecord struct {
	RunID      string     `gorm:"primaryKey;type:varchar(64)"`
	ClaimToken string     `gorm:"index;type:varchar(64)"`
	ClaimUntil *time.Time `gorm:"index"`
}

func (asyncRunLeaseRecord) TableName() string { return "workflow_async_run_leases" }

func (s *GormStore) SaveAsyncTask(ctx context.Context, task *AsyncTask) error {
	if s == nil || s.db == nil {
		return errors.New("gorm store is not configured")
	}
	if task == nil || task.DispatchID == "" || task.RunID == "" || task.NodeID == "" || task.ExternalTaskID == "" {
		return errors.New("async task is incomplete")
	}
	payload, err := json.Marshal(task.Task)
	if err != nil {
		return fmt.Errorf("marshal async task: %w", err)
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table(s.asyncLeasesTable).Clauses(clause.OnConflict{DoNothing: true}).Create(&asyncRunLeaseRecord{RunID: task.RunID}).Error; err != nil {
			return err
		}
		return tx.Table(s.asyncTasksTable).Clauses(clause.OnConflict{DoNothing: true}).Create(&asyncTaskRecord{
			DispatchID: task.DispatchID, RunID: task.RunID, NodeID: task.NodeID,
			ExternalTaskID: task.ExternalTaskID, Task: datatypes.JSON(payload),
			State: string(AsyncTaskWaiting), CreatedAt: now, UpdatedAt: now,
		}).Error
	})
}

func (s *GormStore) SubmitAsyncResult(ctx context.Context, dispatchID string, result executor.ExecuteResult) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("gorm store is not configured")
	}
	if dispatchID == "" {
		return false, errors.New("dispatch_id is required")
	}
	payload, hash, err := asyncResultPayload(result)
	if err != nil {
		return false, err
	}
	accepted := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updated := tx.Table(s.asyncTasksTable).
			Where(map[string]any{"dispatch_id": dispatchID, "state": string(AsyncTaskWaiting)}).
			Updates(map[string]any{"result": datatypes.JSON(payload), "result_hash": hash, "state": string(AsyncTaskCompleted), "updated_at": time.Now().UTC()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 1 {
			accepted = true
			return nil
		}
		var existing asyncTaskRecord
		if err := tx.Table(s.asyncTasksTable).Where(map[string]any{"dispatch_id": dispatchID}).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAsyncTaskNotFound
			}
			return err
		}
		if existing.State == string(AsyncTaskCancelled) {
			return ErrAsyncTaskCancelled
		}
		if existing.ResultHash != hash {
			return ErrAsyncResultConflict
		}
		return nil
	})
	return accepted, err
}

func (s *GormStore) ClaimCompletedAsyncTasks(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]*AsyncTask, error) {
	if limit <= 0 || lease <= 0 {
		return nil, errors.New("async claim limit and lease must be positive")
	}
	claimed := make([]*AsyncTask, 0, limit)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidates []asyncTaskRecord
		if err := tx.Table(s.asyncTasksTable).
			Where("state = ? OR (state = ? AND claim_until <= ?)", AsyncTaskCompleted, AsyncTaskClaimed, now).
			Order("updated_at asc").Limit(limit * 2).Find(&candidates).Error; err != nil {
			return err
		}
		for _, record := range candidates {
			if len(claimed) >= limit {
				break
			}
			token, err := newAsyncClaimToken()
			if err != nil {
				return err
			}
			claimUntil := now.Add(lease).UTC()
			runLease := tx.Table(s.asyncLeasesTable).
				Where("run_id = ? AND (claim_token = '' OR claim_until IS NULL OR claim_until <= ?)", record.RunID, now).
				Updates(map[string]any{"claim_token": token, "claim_until": claimUntil})
			if runLease.Error != nil {
				return runLease.Error
			}
			if runLease.RowsAffected != 1 {
				continue
			}
			taskClaim := tx.Table(s.asyncTasksTable).
				Where("dispatch_id = ? AND (state = ? OR (state = ? AND claim_until <= ?))", record.DispatchID, AsyncTaskCompleted, AsyncTaskClaimed, now).
				Updates(map[string]any{"state": string(AsyncTaskClaimed), "claim_token": token, "claim_until": claimUntil})
			if taskClaim.Error != nil {
				return taskClaim.Error
			}
			if taskClaim.RowsAffected != 1 {
				_ = tx.Table(s.asyncLeasesTable).Where(map[string]any{"run_id": record.RunID, "claim_token": token}).Updates(map[string]any{"claim_token": "", "claim_until": nil}).Error
				continue
			}
			task, err := unmarshalAsyncTask(record)
			if err != nil {
				return err
			}
			task.State = AsyncTaskClaimed
			task.ClaimToken = token
			task.ClaimUntil = claimUntil
			claimed = append(claimed, task)
		}
		return nil
	})
	return claimed, err
}

func (s *GormStore) AcknowledgeAsyncTask(ctx context.Context, dispatchID, claimToken string) error {
	return s.finishAsyncClaim(ctx, dispatchID, claimToken, AsyncTaskDone)
}

func (s *GormStore) RenewAsyncTask(ctx context.Context, dispatchID, claimToken string, claimUntil time.Time) (bool, error) {
	renewed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record asyncTaskRecord
		if err := tx.Table(s.asyncTasksTable).Where(map[string]any{"dispatch_id": dispatchID, "state": string(AsyncTaskClaimed), "claim_token": claimToken}).First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		result := tx.Table(s.asyncLeasesTable).Where(map[string]any{"run_id": record.RunID, "claim_token": claimToken}).Update("claim_until", claimUntil.UTC())
		if result.Error != nil || result.RowsAffected != 1 {
			return result.Error
		}
		result = tx.Table(s.asyncTasksTable).Where(map[string]any{"dispatch_id": dispatchID, "state": string(AsyncTaskClaimed), "claim_token": claimToken}).Update("claim_until", claimUntil.UTC())
		if result.Error != nil {
			return result.Error
		}
		renewed = result.RowsAffected == 1
		return nil
	})
	return renewed, err
}

func (s *GormStore) ReleaseAsyncTask(ctx context.Context, dispatchID, claimToken string) error {
	return s.finishAsyncClaim(ctx, dispatchID, claimToken, AsyncTaskCompleted)
}

func (s *GormStore) finishAsyncClaim(ctx context.Context, dispatchID, claimToken string, state AsyncTaskState) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record asyncTaskRecord
		if err := tx.Table(s.asyncTasksTable).Where(map[string]any{"dispatch_id": dispatchID, "claim_token": claimToken}).First(&record).Error; err != nil {
			return err
		}
		if err := tx.Table(s.asyncTasksTable).Where(map[string]any{"dispatch_id": dispatchID, "claim_token": claimToken}).
			Updates(map[string]any{"state": string(state), "claim_token": "", "claim_until": nil, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.Table(s.asyncLeasesTable).Where(map[string]any{"run_id": record.RunID, "claim_token": claimToken}).
			Updates(map[string]any{"claim_token": "", "claim_until": nil}).Error
	})
}

func (s *GormStore) CancelAsyncTasks(ctx context.Context, runID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table(s.asyncTasksTable).Where("run_id = ? AND state IN ?", runID, []string{string(AsyncTaskWaiting), string(AsyncTaskCompleted), string(AsyncTaskClaimed)}).
			Updates(map[string]any{"state": string(AsyncTaskCancelled), "claim_token": "", "claim_until": nil, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.Table(s.asyncLeasesTable).Where(map[string]any{"run_id": runID}).Updates(map[string]any{"claim_token": "", "claim_until": nil}).Error
	})
}

func unmarshalAsyncTask(record asyncTaskRecord) (*AsyncTask, error) {
	var task executor.ExecuteTask
	if err := json.Unmarshal(record.Task, &task); err != nil {
		return nil, fmt.Errorf("unmarshal async task: %w", err)
	}
	var result executor.ExecuteResult
	if len(record.Result) == 0 {
		return nil, errors.New("completed async task has no result")
	}
	if err := json.Unmarshal(record.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal async result: %w", err)
	}
	return &AsyncTask{DispatchID: record.DispatchID, RunID: record.RunID, NodeID: record.NodeID, ExternalTaskID: record.ExternalTaskID, Task: task, Result: &result, ResultHash: record.ResultHash, State: AsyncTaskState(record.State), CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, nil
}

var _ AsyncTaskStore = (*GormStore)(nil)
