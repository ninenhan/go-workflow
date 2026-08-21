package definition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const WorkspaceSchemaVersion = 1

var (
	ErrWorkspaceNotFound = errors.New("workspace not found")
	ErrWorkspaceConflict = errors.New("workspace revision conflict")
)

// Workspace is the server-owned editable state. Executable data is strongly
// typed as WorkflowDefinition; editor-only layout and service presentation data
// remain isolated JSON extensions and never enter the compiler directly.
type Workspace struct {
	SchemaVersion  int               `json:"schema_version"`
	Revision       uint64            `json:"revision"`
	ActiveID       string            `json:"activeId"`
	Folders        []WorkspaceFolder `json:"folders"`
	Entries        []WorkspaceEntry  `json:"entries"`
	ServiceActions []map[string]any  `json:"serviceActions"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type WorkspaceFolder struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

type WorkspaceEntry struct {
	ID       string            `json:"id"`
	SavedAt  time.Time         `json:"savedAt"`
	FolderID string            `json:"folderId,omitempty"`
	Document WorkspaceDocument `json:"document"`
}

type WorkspaceDocument struct {
	Definition *WorkflowDefinition `json:"definition"`
	Layout     map[string]any      `json:"layout,omitempty"`
}

type WorkspaceRepository interface {
	GetWorkspace(ctx context.Context) (*Workspace, error)
	SaveWorkspace(ctx context.Context, workspace *Workspace, expectedRevision uint64) (*Workspace, error)
}

func ValidateWorkspace(workspace *Workspace) error {
	if workspace == nil {
		return errors.New("workspace is required")
	}
	if workspace.SchemaVersion != WorkspaceSchemaVersion {
		return fmt.Errorf("unsupported workspace schema version %d", workspace.SchemaVersion)
	}
	if len(workspace.Entries) == 0 {
		return errors.New("workspace requires at least one shortcut")
	}

	folders := make(map[string]struct{}, len(workspace.Folders))
	for index, folder := range workspace.Folders {
		folderID := strings.TrimSpace(folder.ID)
		if folderID == "" {
			return fmt.Errorf("workspace folder %d id is required", index+1)
		}
		if strings.TrimSpace(folder.Name) == "" {
			return fmt.Errorf("workspace folder %s name is required", folderID)
		}
		if _, exists := folders[folderID]; exists {
			return fmt.Errorf("workspace folder id %s is duplicated", folderID)
		}
		folders[folderID] = struct{}{}
	}

	entries := make(map[string]struct{}, len(workspace.Entries))
	for index, entry := range workspace.Entries {
		entryID := strings.TrimSpace(entry.ID)
		if entryID == "" {
			return fmt.Errorf("workspace shortcut %d id is required", index+1)
		}
		if _, exists := entries[entryID]; exists {
			return fmt.Errorf("workspace shortcut id %s is duplicated", entryID)
		}
		entries[entryID] = struct{}{}
		if entry.FolderID != "" {
			if _, exists := folders[entry.FolderID]; !exists {
				return fmt.Errorf("workspace shortcut %s references missing folder %s", entryID, entry.FolderID)
			}
		}
		if entry.Document.Definition == nil {
			return fmt.Errorf("workspace shortcut %s definition is required", entryID)
		}
		if entry.Document.Definition.ID != "" && entry.Document.Definition.ID != entryID {
			return fmt.Errorf("workspace shortcut %s definition id does not match", entryID)
		}
	}
	if _, exists := entries[workspace.ActiveID]; !exists {
		return fmt.Errorf("workspace active shortcut %s does not exist", workspace.ActiveID)
	}
	for index, action := range workspace.ServiceActions {
		if action == nil {
			return fmt.Errorf("workspace service action %d must be an object", index+1)
		}
		id, _ := action["id"].(string)
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("workspace service action %d id is required", index+1)
		}
	}
	return nil
}

func cloneWorkspace(workspace *Workspace) (*Workspace, error) {
	raw, err := json.Marshal(workspace)
	if err != nil {
		return nil, fmt.Errorf("marshal workspace: %w", err)
	}
	var cloned Workspace
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return nil, fmt.Errorf("unmarshal workspace: %w", err)
	}
	return &cloned, nil
}

type MemoryWorkspaceRepository struct {
	mu        sync.RWMutex
	workspace *Workspace
}

func NewMemoryWorkspaceRepository() *MemoryWorkspaceRepository {
	return &MemoryWorkspaceRepository{}
}

func (r *MemoryWorkspaceRepository) GetWorkspace(context.Context) (*Workspace, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.workspace == nil {
		return nil, ErrWorkspaceNotFound
	}
	return cloneWorkspace(r.workspace)
}

func (r *MemoryWorkspaceRepository) SaveWorkspace(_ context.Context, workspace *Workspace, expectedRevision uint64) (*Workspace, error) {
	if err := ValidateWorkspace(workspace); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	currentRevision := uint64(0)
	if r.workspace != nil {
		currentRevision = r.workspace.Revision
	}
	if currentRevision != expectedRevision {
		return nil, ErrWorkspaceConflict
	}
	cloned, err := cloneWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	cloned.Revision = currentRevision + 1
	cloned.UpdatedAt = time.Now().UTC()
	r.workspace = cloned
	return cloneWorkspace(cloned)
}
