package wfruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GormStore struct {
	db *gorm.DB
}

const InterruptedRunMessage = "server restarted before the run completed"

type workflowRunRecord struct {
	ID                 string         `gorm:"primaryKey;type:varchar(64)"`
	WorkflowID         string         `gorm:"index;type:varchar(128);not null"`
	WorkflowVersionID  string         `gorm:"index;type:varchar(128);not null"`
	PlanID             string         `gorm:"type:varchar(128);not null"`
	RequestFingerprint string         `gorm:"type:varchar(128)"`
	CredentialScope    string         `gorm:"type:varchar(128)"`
	Status             string         `gorm:"index;type:varchar(32);not null"`
	CurrentNodes       datatypes.JSON `gorm:"type:json"`
	NodeRuns           datatypes.JSON `gorm:"type:json"`
	Context            datatypes.JSON `gorm:"type:json"`
	CreatedAt          time.Time      `gorm:"index"`
	UpdatedAt          time.Time      `gorm:"index"`
	StartedAt          *time.Time
	FinishedAt         *time.Time
}

func (workflowRunRecord) TableName() string { return "workflow_runs" }

type runSnapshotRecord struct {
	ID          uint           `gorm:"primaryKey"`
	RunID       string         `gorm:"index;type:varchar(64);not null"`
	Status      string         `gorm:"index;type:varchar(32);not null"`
	NodeRuns    datatypes.JSON `gorm:"type:json"`
	Context     datatypes.JSON `gorm:"type:json"`
	At          time.Time      `gorm:"index"`
	Description string         `gorm:"type:text"`
}

func (runSnapshotRecord) TableName() string { return "workflow_run_snapshots" }

type runEventRecord struct {
	ID         uint           `gorm:"primaryKey"`
	RunID      string         `gorm:"index;type:varchar(64);not null"`
	WorkflowID string         `gorm:"index;type:varchar(128)"`
	Type       string         `gorm:"index;type:varchar(64);not null"`
	NodeID     string         `gorm:"index;type:varchar(128)"`
	Status     string         `gorm:"index;type:varchar(32)"`
	Time       time.Time      `gorm:"index"`
	Message    string         `gorm:"type:text"`
	Payload    datatypes.JSON `gorm:"type:json"`
}

func (runEventRecord) TableName() string { return "workflow_run_events" }

func NewGormStore(db *gorm.DB) (*GormStore, error) {
	if db == nil {
		return nil, errors.New("gorm db is nil")
	}
	if err := db.AutoMigrate(&workflowRunRecord{}, &runSnapshotRecord{}, &runEventRecord{}); err != nil {
		return nil, fmt.Errorf("auto migrate runtime store: %w", err)
	}
	return &GormStore{db: db}, nil
}

func (s *GormStore) SaveRun(ctx context.Context, run *WorkflowRun) error {
	if s == nil || s.db == nil {
		return errors.New("gorm store is not configured")
	}
	if run == nil {
		return errors.New("run is nil")
	}
	record, err := marshalRun(run)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		UpdateAll: true,
	}).Create(record).Error
}

func (s *GormStore) LoadRun(ctx context.Context, runID string) (*WorkflowRun, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("gorm store is not configured")
	}
	var record workflowRunRecord
	if err := s.db.WithContext(ctx).First(&record, "id = ?", runID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRunNotFound
		}
		return nil, err
	}
	return unmarshalRun(record)
}

func (s *GormStore) ListRuns(ctx context.Context) ([]*WorkflowRun, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("gorm store is not configured")
	}
	var records []workflowRunRecord
	if err := s.db.WithContext(ctx).Order("created_at desc").Find(&records).Error; err != nil {
		return nil, err
	}
	out := make([]*WorkflowRun, 0, len(records))
	for _, record := range records {
		run, err := unmarshalRun(record)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

func (s *GormStore) SaveSnapshot(ctx context.Context, snapshot *RunSnapshot) error {
	if s == nil || s.db == nil {
		return errors.New("gorm store is not configured")
	}
	if snapshot == nil {
		return errors.New("snapshot is nil")
	}
	record, err := marshalSnapshot(snapshot)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Create(record).Error
}

func (s *GormStore) Snapshots(ctx context.Context, runID string) ([]*RunSnapshot, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("gorm store is not configured")
	}
	var records []runSnapshotRecord
	if err := s.db.WithContext(ctx).Where("run_id = ?", runID).Order("at asc, id asc").Find(&records).Error; err != nil {
		return nil, err
	}
	out := make([]*RunSnapshot, 0, len(records))
	for _, record := range records {
		snapshot, err := unmarshalSnapshot(record)
		if err != nil {
			return nil, err
		}
		out = append(out, snapshot)
	}
	return out, nil
}

func (s *GormStore) AppendEvent(ctx context.Context, event RunEvent) error {
	if s == nil || s.db == nil {
		return errors.New("gorm store is not configured")
	}
	record, err := marshalEvent(event)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Create(record).Error
}

func (s *GormStore) Events(ctx context.Context, runID string) ([]RunEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("gorm store is not configured")
	}
	var records []runEventRecord
	if err := s.db.WithContext(ctx).Where("run_id = ?", runID).Order("time asc, id asc").Find(&records).Error; err != nil {
		return nil, err
	}
	out := make([]RunEvent, 0, len(records))
	for _, record := range records {
		event, err := unmarshalEvent(record)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, nil
}

func (s *GormStore) FailInterruptedRuns(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("gorm store is not configured")
	}
	recovered := 0
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var records []workflowRunRecord
		activeStatuses := []string{
			string(StatusPending),
			string(StatusRunning),
			string(StatusRetry),
			string(StatusPaused),
		}
		if err := tx.Where("status IN ?", activeStatuses).Order("id asc").Find(&records).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		for _, record := range records {
			run, err := unmarshalRun(record)
			if err != nil {
				return err
			}
			for _, node := range run.NodeRuns {
				if node == nil {
					continue
				}
				switch node.Status {
				case StatusRunning, StatusRetry, StatusPaused:
					node.Status = StatusFailed
					node.Error = InterruptedRunMessage
					node.FinishedAt = now
				case StatusPending:
					node.Status = StatusCancelled
					node.FinishedAt = now
				}
			}
			run.Status = StatusFailed
			run.CurrentNodes = nil
			run.UpdatedAt = now
			run.FinishedAt = now
			updated, err := marshalRun(run)
			if err != nil {
				return err
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}},
				UpdateAll: true,
			}).Create(updated).Error; err != nil {
				return err
			}
			event, err := marshalEvent(RunEvent{
				RunID:      run.ID,
				WorkflowID: run.WorkflowID,
				Type:       EventRunFinished,
				Status:     StatusFailed,
				Time:       now,
				Message:    InterruptedRunMessage,
			})
			if err != nil {
				return err
			}
			if err := tx.Create(event).Error; err != nil {
				return err
			}
			recovered++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("fail interrupted workflow runs: %w", err)
	}
	return recovered, nil
}

func marshalRun(run *WorkflowRun) (*workflowRunRecord, error) {
	record := &workflowRunRecord{
		ID:                 run.ID,
		WorkflowID:         run.WorkflowID,
		WorkflowVersionID:  run.WorkflowVersionID,
		PlanID:             run.PlanID,
		RequestFingerprint: run.RequestFingerprint,
		CredentialScope:    run.CredentialScope,
		Status:             string(run.Status),
		CreatedAt:          run.CreatedAt,
		UpdatedAt:          run.UpdatedAt,
	}
	if !run.StartedAt.IsZero() {
		startedAt := run.StartedAt
		record.StartedAt = &startedAt
	}
	if !run.FinishedAt.IsZero() {
		finishedAt := run.FinishedAt
		record.FinishedAt = &finishedAt
	}
	var err error
	if record.CurrentNodes, err = marshalJSON(run.CurrentNodes); err != nil {
		return nil, fmt.Errorf("marshal current nodes: %w", err)
	}
	if record.NodeRuns, err = marshalJSON(run.NodeRuns); err != nil {
		return nil, fmt.Errorf("marshal node runs: %w", err)
	}
	if record.Context, err = marshalJSON(run.Context); err != nil {
		return nil, fmt.Errorf("marshal run context: %w", err)
	}
	return record, nil
}

func unmarshalRun(record workflowRunRecord) (*WorkflowRun, error) {
	run := &WorkflowRun{
		ID:                 record.ID,
		WorkflowID:         record.WorkflowID,
		WorkflowVersionID:  record.WorkflowVersionID,
		PlanID:             record.PlanID,
		RequestFingerprint: record.RequestFingerprint,
		CredentialScope:    record.CredentialScope,
		Status:             Status(record.Status),
		CreatedAt:          record.CreatedAt,
		UpdatedAt:          record.UpdatedAt,
	}
	if record.StartedAt != nil {
		run.StartedAt = *record.StartedAt
	}
	if record.FinishedAt != nil {
		run.FinishedAt = *record.FinishedAt
	}
	if err := unmarshalJSON(record.CurrentNodes, &run.CurrentNodes); err != nil {
		return nil, fmt.Errorf("unmarshal current nodes: %w", err)
	}
	if err := unmarshalJSON(record.NodeRuns, &run.NodeRuns); err != nil {
		return nil, fmt.Errorf("unmarshal node runs: %w", err)
	}
	if err := unmarshalJSON(record.Context, &run.Context); err != nil {
		return nil, fmt.Errorf("unmarshal run context: %w", err)
	}
	if run.NodeRuns == nil {
		run.NodeRuns = map[string]*NodeRun{}
	}
	if run.Context.Variables == nil {
		run.Context.Variables = map[string]any{}
	}
	if run.Context.NodeResults == nil {
		run.Context.NodeResults = map[string]any{}
	}
	return run, nil
}

func marshalSnapshot(snapshot *RunSnapshot) (*runSnapshotRecord, error) {
	record := &runSnapshotRecord{
		RunID:       snapshot.RunID,
		Status:      string(snapshot.Status),
		At:          snapshot.At,
		Description: snapshot.Description,
	}
	var err error
	if record.NodeRuns, err = marshalJSON(snapshot.NodeRuns); err != nil {
		return nil, fmt.Errorf("marshal snapshot node runs: %w", err)
	}
	if record.Context, err = marshalJSON(snapshot.Context); err != nil {
		return nil, fmt.Errorf("marshal snapshot context: %w", err)
	}
	return record, nil
}

func unmarshalSnapshot(record runSnapshotRecord) (*RunSnapshot, error) {
	snapshot := &RunSnapshot{
		RunID:       record.RunID,
		Status:      Status(record.Status),
		At:          record.At,
		Description: record.Description,
	}
	if err := unmarshalJSON(record.NodeRuns, &snapshot.NodeRuns); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot node runs: %w", err)
	}
	if err := unmarshalJSON(record.Context, &snapshot.Context); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot context: %w", err)
	}
	if snapshot.NodeRuns == nil {
		snapshot.NodeRuns = map[string]*NodeRun{}
	}
	if snapshot.Context.Variables == nil {
		snapshot.Context.Variables = map[string]any{}
	}
	if snapshot.Context.NodeResults == nil {
		snapshot.Context.NodeResults = map[string]any{}
	}
	return snapshot, nil
}

func marshalEvent(event RunEvent) (*runEventRecord, error) {
	payload, err := marshalJSON(event.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal event payload: %w", err)
	}
	return &runEventRecord{
		RunID:      event.RunID,
		WorkflowID: event.WorkflowID,
		Type:       string(event.Type),
		NodeID:     event.NodeID,
		Status:     string(event.Status),
		Time:       event.Time,
		Message:    event.Message,
		Payload:    payload,
	}, nil
}

func unmarshalEvent(record runEventRecord) (RunEvent, error) {
	event := RunEvent{
		RunID:      record.RunID,
		WorkflowID: record.WorkflowID,
		Type:       EventType(record.Type),
		NodeID:     record.NodeID,
		Status:     Status(record.Status),
		Time:       record.Time,
		Message:    record.Message,
	}
	if err := unmarshalJSON(record.Payload, &event.Payload); err != nil {
		return RunEvent{}, fmt.Errorf("unmarshal event payload: %w", err)
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	return event, nil
}

func marshalJSON(v any) (datatypes.JSON, error) {
	if v == nil {
		return datatypes.JSON([]byte("null")), nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(raw), nil
}

func unmarshalJSON(raw datatypes.JSON, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, out)
}
