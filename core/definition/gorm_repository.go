package definition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GormRepository struct {
	db *gorm.DB
	mu sync.Mutex
}

type workflowRecord struct {
	ID            string         `gorm:"primaryKey;type:varchar(128)"`
	Name          string         `gorm:"type:varchar(255);not null"`
	Description   string         `gorm:"type:text"`
	ActiveVersion string         `gorm:"index;type:varchar(160)"`
	Tags          datatypes.JSON `gorm:"type:json"`
	Metadata      datatypes.JSON `gorm:"type:json"`
	CreatedAt     time.Time      `gorm:"index;not null"`
	UpdatedAt     time.Time      `gorm:"index;not null"`
}

func (workflowRecord) TableName() string { return "workflow_definitions" }

type workflowVersionRecord struct {
	ID         string         `gorm:"primaryKey;type:varchar(160)"`
	WorkflowID string         `gorm:"uniqueIndex:idx_workflow_version;index;type:varchar(128);not null"`
	Version    int            `gorm:"uniqueIndex:idx_workflow_version;not null"`
	Status     string         `gorm:"index;type:varchar(32);not null"`
	Definition datatypes.JSON `gorm:"type:json;not null"`
	CreatedAt  time.Time      `gorm:"index;not null"`
}

func (workflowVersionRecord) TableName() string { return "workflow_versions" }

func NewGormRepository(db *gorm.DB) (*GormRepository, error) {
	if db == nil {
		return nil, errors.New("gorm db is nil")
	}
	if err := db.AutoMigrate(&workflowRecord{}, &workflowVersionRecord{}); err != nil {
		return nil, fmt.Errorf("auto migrate definition repository: %w", err)
	}
	return &GormRepository{db: db}, nil
}

func (r *GormRepository) SaveWorkflow(ctx context.Context, workflow *Workflow) error {
	if err := validateWorkflow(workflow); err != nil {
		return err
	}
	record, err := marshalWorkflowRecord(workflow)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	record.UpdatedAt = now

	r.mu.Lock()
	defer r.mu.Unlock()

	var existing workflowRecord
	queryErr := r.db.WithContext(ctx).First(&existing, "id = ?", record.ID).Error
	switch {
	case queryErr == nil:
		record.CreatedAt = existing.CreatedAt
	case errors.Is(queryErr, gorm.ErrRecordNotFound):
		if record.CreatedAt.IsZero() {
			record.CreatedAt = now
		}
	default:
		return queryErr
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		UpdateAll: true,
	}).Create(record).Error
}

func (r *GormRepository) GetWorkflow(ctx context.Context, workflowID string) (*Workflow, error) {
	if workflowID == "" {
		return nil, errors.New("workflow id is required")
	}
	var record workflowRecord
	if err := r.db.WithContext(ctx).First(&record, "id = ?", workflowID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("workflow not found")
		}
		return nil, err
	}
	return unmarshalWorkflowRecord(record)
}

func (r *GormRepository) ListWorkflows(ctx context.Context) ([]*Workflow, error) {
	var records []workflowRecord
	if err := r.db.WithContext(ctx).Order("id asc").Find(&records).Error; err != nil {
		return nil, err
	}
	workflows := make([]*Workflow, 0, len(records))
	for _, record := range records {
		workflow, err := unmarshalWorkflowRecord(record)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, workflow)
	}
	return workflows, nil
}

func (r *GormRepository) CreateVersion(ctx context.Context, workflowID string, definition *WorkflowDefinition) (*WorkflowVersion, error) {
	if workflowID == "" {
		return nil, errors.New("workflow id is required")
	}
	if definition == nil {
		return nil, errors.New("workflow definition is required")
	}
	definitionCopy, definitionJSON, err := cloneAndMarshalDefinition(workflowID, definition)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var created *WorkflowVersion
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var maxVersion int
		if err := tx.Model(&workflowVersionRecord{}).
			Where("workflow_id = ?", workflowID).
			Select("COALESCE(MAX(version), 0)").
			Scan(&maxVersion).Error; err != nil {
			return err
		}
		nextVersion := maxVersion + 1
		now := time.Now().UTC()
		record := workflowVersionRecord{
			ID:         fmt.Sprintf("%s:v%d", workflowID, nextVersion),
			WorkflowID: workflowID,
			Version:    nextVersion,
			Status:     string(VersionDraft),
			Definition: definitionJSON,
			CreatedAt:  now,
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		if err := ensureWorkflowRecord(tx, workflowID, definitionCopy, now); err != nil {
			return err
		}
		created = &WorkflowVersion{
			ID:         record.ID,
			WorkflowID: workflowID,
			Version:    nextVersion,
			Status:     VersionDraft,
			Definition: definitionCopy,
			CreatedAt:  now,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (r *GormRepository) SaveVersion(ctx context.Context, version *WorkflowVersion) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	record, err := marshalVersionRecord(version)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing workflowVersionRecord
		queryErr := tx.First(&existing, "id = ?", record.ID).Error
		switch {
		case queryErr == nil:
			if existing.WorkflowID != record.WorkflowID || existing.Version != record.Version {
				return errors.New("workflow version identity cannot be changed")
			}
			record.CreatedAt = existing.CreatedAt
		case errors.Is(queryErr, gorm.ErrRecordNotFound):
			if record.CreatedAt.IsZero() {
				record.CreatedAt = time.Now().UTC()
			}
		default:
			return queryErr
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			UpdateAll: true,
		}).Create(record).Error; err != nil {
			return err
		}
		return ensureWorkflowRecord(tx, version.WorkflowID, version.Definition, time.Now().UTC())
	})
}

func (r *GormRepository) GetVersion(ctx context.Context, versionID string) (*WorkflowVersion, error) {
	if versionID == "" {
		return nil, errors.New("workflow version id is required")
	}
	var record workflowVersionRecord
	if err := r.db.WithContext(ctx).First(&record, "id = ?", versionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("workflow version not found")
		}
		return nil, err
	}
	return unmarshalVersionRecord(record)
}

func (r *GormRepository) ListVersions(ctx context.Context, workflowID string) ([]*WorkflowVersion, error) {
	if workflowID == "" {
		return nil, errors.New("workflow id is required")
	}
	var records []workflowVersionRecord
	if err := r.db.WithContext(ctx).
		Where("workflow_id = ?", workflowID).
		Order("version asc, id asc").
		Find(&records).Error; err != nil {
		return nil, err
	}
	versions := make([]*WorkflowVersion, 0, len(records))
	for _, record := range records {
		version, err := unmarshalVersionRecord(record)
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, nil
}

func (r *GormRepository) GetActiveVersion(ctx context.Context, workflowID string) (*WorkflowVersion, error) {
	workflow, err := r.GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if workflow.ActiveVersion == "" {
		return nil, errors.New("workflow has no active version")
	}
	var record workflowVersionRecord
	if err := r.db.WithContext(ctx).First(&record, "id = ?", workflow.ActiveVersion).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("active workflow version not found")
		}
		return nil, err
	}
	return unmarshalVersionRecord(record)
}

func (r *GormRepository) PublishVersion(ctx context.Context, versionID string) (*WorkflowVersion, error) {
	if versionID == "" {
		return nil, errors.New("workflow version id is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var published *WorkflowVersion
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record workflowVersionRecord
		if err := tx.First(&record, "id = ?", versionID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("workflow version not found")
			}
			return err
		}
		var workflow workflowRecord
		if err := tx.First(&workflow, "id = ?", record.WorkflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("workflow not found for version %s", versionID)
			}
			return err
		}
		if err := tx.Model(&workflowVersionRecord{}).
			Where("workflow_id = ? AND id <> ? AND status = ?", record.WorkflowID, versionID, VersionPublished).
			Update("status", VersionArchived).Error; err != nil {
			return err
		}
		if err := tx.Model(&workflowVersionRecord{}).
			Where("id = ?", versionID).
			Update("status", VersionPublished).Error; err != nil {
			return err
		}
		if err := tx.Model(&workflowRecord{}).
			Where("id = ?", record.WorkflowID).
			Updates(map[string]any{
				"active_version": versionID,
				"updated_at":     time.Now().UTC(),
			}).Error; err != nil {
			return err
		}
		record.Status = string(VersionPublished)
		var err error
		published, err = unmarshalVersionRecord(record)
		return err
	})
	if err != nil {
		return nil, err
	}
	return published, nil
}

func validateWorkflow(workflow *Workflow) error {
	if workflow == nil {
		return errors.New("workflow is nil")
	}
	if workflow.ID == "" {
		return errors.New("workflow id is required")
	}
	return nil
}

func validateVersion(version *WorkflowVersion) error {
	if version == nil || version.Definition == nil {
		return errors.New("workflow version is nil")
	}
	if version.ID == "" {
		return errors.New("workflow version id is required")
	}
	if version.WorkflowID == "" {
		return errors.New("workflow id is required")
	}
	if version.Version <= 0 {
		return errors.New("workflow version number must be positive")
	}
	return nil
}

func ensureWorkflowRecord(tx *gorm.DB, workflowID string, definition *WorkflowDefinition, now time.Time) error {
	var workflow workflowRecord
	err := tx.First(&workflow, "id = ?", workflowID).Error
	switch {
	case err == nil:
		return tx.Model(&workflowRecord{}).
			Where("id = ?", workflowID).
			Update("updated_at", now).Error
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return err
	}
	tags, err := json.Marshal([]string{})
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(map[string]any{})
	if err != nil {
		return err
	}
	return tx.Create(&workflowRecord{
		ID:          workflowID,
		Name:        definition.Name,
		Description: definition.Description,
		Tags:        tags,
		Metadata:    metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error
}

func marshalWorkflowRecord(workflow *Workflow) (*workflowRecord, error) {
	tags, err := json.Marshal(workflow.Tags)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow tags: %w", err)
	}
	metadata, err := json.Marshal(workflow.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow metadata: %w", err)
	}
	return &workflowRecord{
		ID:            workflow.ID,
		Name:          workflow.Name,
		Description:   workflow.Description,
		ActiveVersion: workflow.ActiveVersion,
		Tags:          tags,
		Metadata:      metadata,
		CreatedAt:     workflow.CreatedAt,
		UpdatedAt:     workflow.UpdatedAt,
	}, nil
}

func unmarshalWorkflowRecord(record workflowRecord) (*Workflow, error) {
	workflow := &Workflow{
		ID:            record.ID,
		Name:          record.Name,
		Description:   record.Description,
		ActiveVersion: record.ActiveVersion,
		CreatedAt:     record.CreatedAt,
		UpdatedAt:     record.UpdatedAt,
	}
	if err := unmarshalDefinitionJSON(record.Tags, &workflow.Tags); err != nil {
		return nil, fmt.Errorf("unmarshal workflow tags: %w", err)
	}
	if err := unmarshalDefinitionJSON(record.Metadata, &workflow.Metadata); err != nil {
		return nil, fmt.Errorf("unmarshal workflow metadata: %w", err)
	}
	return workflow, nil
}

func marshalVersionRecord(version *WorkflowVersion) (*workflowVersionRecord, error) {
	_, definitionJSON, err := cloneAndMarshalDefinition(version.WorkflowID, version.Definition)
	if err != nil {
		return nil, err
	}
	status := version.Status
	if status == "" {
		status = VersionDraft
	}
	return &workflowVersionRecord{
		ID:         version.ID,
		WorkflowID: version.WorkflowID,
		Version:    version.Version,
		Status:     string(status),
		Definition: definitionJSON,
		CreatedAt:  version.CreatedAt,
	}, nil
}

func unmarshalVersionRecord(record workflowVersionRecord) (*WorkflowVersion, error) {
	var workflowDefinition WorkflowDefinition
	if err := unmarshalDefinitionJSON(record.Definition, &workflowDefinition); err != nil {
		return nil, fmt.Errorf("unmarshal workflow version definition: %w", err)
	}
	if workflowDefinition.ID != record.WorkflowID {
		return nil, fmt.Errorf("workflow version %s definition id does not match workflow id", record.ID)
	}
	return &WorkflowVersion{
		ID:         record.ID,
		WorkflowID: record.WorkflowID,
		Version:    record.Version,
		Status:     VersionStatus(record.Status),
		Definition: &workflowDefinition,
		CreatedAt:  record.CreatedAt,
	}, nil
}

func cloneAndMarshalDefinition(workflowID string, definition *WorkflowDefinition) (*WorkflowDefinition, datatypes.JSON, error) {
	raw, err := json.Marshal(definition)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal workflow definition: %w", err)
	}
	var cloned WorkflowDefinition
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return nil, nil, fmt.Errorf("clone workflow definition: %w", err)
	}
	cloned.ID = workflowID
	raw, err = json.Marshal(&cloned)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal normalized workflow definition: %w", err)
	}
	return &cloned, datatypes.JSON(raw), nil
}

func unmarshalDefinitionJSON(raw datatypes.JSON, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, target)
}

var _ Repository = (*GormRepository)(nil)
