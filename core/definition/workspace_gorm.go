package definition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ninenhan/go-workflow/internal/gormdb"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultWorkspaceRecordID = "default"

type workspaceRecord struct {
	ID        string         `gorm:"primaryKey;size:64"`
	Revision  uint64         `gorm:"not null"`
	Document  datatypes.JSON `gorm:"not null"`
	UpdatedAt time.Time      `gorm:"not null"`
}

func (workspaceRecord) TableName() string { return "workflow_workspaces" }

type GormWorkspaceRepository struct {
	db        *gorm.DB
	tableName string
}

// NewGormWorkspaceRepository constructs the legacy standalone workspace adapter.
//
// Deprecated: new applications should compose the complete adapter through
// persist/gormstore.New. This constructor remains available for compatibility.
func NewGormWorkspaceRepository(db *gorm.DB) (*GormWorkspaceRepository, error) {
	return NewGormWorkspaceRepositoryWithTablePrefix(db, "")
}

func NewGormWorkspaceRepositoryWithTablePrefix(db *gorm.DB, tablePrefix string) (*GormWorkspaceRepository, error) {
	if db == nil {
		return nil, errors.New("workspace database is required")
	}
	if err := gormdb.ValidateTablePrefix(tablePrefix); err != nil {
		return nil, fmt.Errorf("configure workspace store: %w", err)
	}
	tableName := gormdb.TableName(tablePrefix, workspaceRecord{}.TableName())
	if err := db.Table(tableName).AutoMigrate(&workspaceRecord{}); err != nil {
		return nil, fmt.Errorf("migrate workspace store: %w", err)
	}
	return &GormWorkspaceRepository{db: db, tableName: tableName}, nil
}

func (r *GormWorkspaceRepository) GetWorkspace(ctx context.Context) (*Workspace, error) {
	var record workspaceRecord
	if err := r.db.WithContext(ctx).Table(r.tableName).First(&record, "id = ?", defaultWorkspaceRecordID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkspaceNotFound
		}
		return nil, fmt.Errorf("load workspace: %w", err)
	}
	var workspace Workspace
	if err := json.Unmarshal(record.Document, &workspace); err != nil {
		return nil, fmt.Errorf("decode workspace: %w", err)
	}
	workspace.Revision = record.Revision
	workspace.UpdatedAt = record.UpdatedAt
	if err := ValidateWorkspace(&workspace); err != nil {
		return nil, fmt.Errorf("stored workspace is invalid: %w", err)
	}
	return &workspace, nil
}

func (r *GormWorkspaceRepository) SaveWorkspace(ctx context.Context, workspace *Workspace, expectedRevision uint64) (*Workspace, error) {
	if err := ValidateWorkspace(workspace); err != nil {
		return nil, err
	}
	cloned, err := cloneWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	cloned.Revision = expectedRevision + 1
	cloned.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(cloned)
	if err != nil {
		return nil, fmt.Errorf("encode workspace: %w", err)
	}

	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if expectedRevision == 0 {
			record := workspaceRecord{ID: defaultWorkspaceRecordID, Revision: 1, Document: raw, UpdatedAt: cloned.UpdatedAt}
			result := tx.Table(r.tableName).Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrWorkspaceConflict
			}
			return nil
		}
		result := tx.Table(r.tableName).Model(&workspaceRecord{}).
			Where("id = ? AND revision = ?", defaultWorkspaceRecordID, expectedRevision).
			Updates(map[string]any{"revision": cloned.Revision, "document": datatypes.JSON(raw), "updated_at": cloned.UpdatedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrWorkspaceConflict
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrWorkspaceConflict) {
			return nil, ErrWorkspaceConflict
		}
		return nil, fmt.Errorf("save workspace: %w", err)
	}
	return cloned, nil
}
