package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	workflow "github.com/ninenhan/go-workflow"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ExecutionStateModel struct {
	ID         uint   `gorm:"primaryKey"`
	WorkflowID string `gorm:"size:128;index:idx_workflow_run,unique"`
	RunID      string `gorm:"size:128;index:idx_workflow_run,unique"`
	Status     string `gorm:"size:64"`
	StartedAt  time.Time
	UpdatedAt  time.Time
	Nodes      datatypes.JSON `gorm:"type:json"`
	CreatedAt  time.Time
	DeletedAt  gorm.DeletedAt `gorm:"index"`
}

type GormStateStore struct {
	db *gorm.DB
}

func NewGormStateStore(db *gorm.DB) (*GormStateStore, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}
	if err := db.AutoMigrate(&ExecutionStateModel{}); err != nil {
		return nil, err
	}
	return &GormStateStore{db: db}, nil
}

func (s *GormStateStore) Save(_ context.Context, state *workflow.ExecutionState) error {
	if s == nil || s.db == nil {
		return errors.New("db not configured")
	}
	if state == nil {
		return errors.New("state is nil")
	}
	nodes, err := json.Marshal(state.Nodes)
	if err != nil {
		return err
	}
	model := ExecutionStateModel{
		WorkflowID: state.WorkflowID,
		RunID:      state.RunID,
		Status:     string(state.Status),
		StartedAt:  state.StartedAt,
		UpdatedAt:  state.UpdatedAt,
		Nodes:      datatypes.JSON(nodes),
	}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "workflow_id"}, {Name: "run_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"status", "started_at", "updated_at", "nodes"}),
	}).Create(&model).Error
}

func (s *GormStateStore) Load(_ context.Context, workflowID, runID string) (*workflow.ExecutionState, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("db not configured")
	}
	var model ExecutionStateModel
	if err := s.db.Where("workflow_id = ? AND run_id = ?", workflowID, runID).First(&model).Error; err != nil {
		return nil, err
	}
	state := &workflow.ExecutionState{
		WorkflowID: model.WorkflowID,
		RunID:      model.RunID,
		Status:     workflow.RunStatus(model.Status),
		StartedAt:  model.StartedAt,
		UpdatedAt:  model.UpdatedAt,
		Nodes:      make(map[string]*workflow.NodeState),
	}
	if len(model.Nodes) > 0 {
		if err := json.Unmarshal(model.Nodes, &state.Nodes); err != nil {
			return nil, err
		}
	}
	return state, nil
}
