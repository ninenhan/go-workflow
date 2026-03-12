package definition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

type Repository interface {
	SaveWorkflow(ctx context.Context, workflow *Workflow) error
	GetWorkflow(ctx context.Context, workflowID string) (*Workflow, error)
	ListWorkflows(ctx context.Context) ([]*Workflow, error)
	SaveVersion(ctx context.Context, version *WorkflowVersion) error
	GetVersion(ctx context.Context, versionID string) (*WorkflowVersion, error)
	ListVersions(ctx context.Context, workflowID string) ([]*WorkflowVersion, error)
	GetActiveVersion(ctx context.Context, workflowID string) (*WorkflowVersion, error)
	PublishVersion(ctx context.Context, versionID string) (*WorkflowVersion, error)
}

type MemoryRepository struct {
	mu        sync.RWMutex
	workflows map[string]*Workflow
	versions  map[string]*WorkflowVersion
	byFlow    map[string][]string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		workflows: make(map[string]*Workflow),
		versions:  make(map[string]*WorkflowVersion),
		byFlow:    make(map[string][]string),
	}
}

func (r *MemoryRepository) SaveWorkflow(_ context.Context, workflow *Workflow) error {
	if workflow == nil {
		return errors.New("workflow is nil")
	}
	if workflow.ID == "" {
		return errors.New("workflow id is required")
	}
	now := time.Now()
	cp := cloneWorkflow(workflow)
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = now
	}
	cp.UpdatedAt = now

	r.mu.Lock()
	if existing := r.workflows[cp.ID]; existing != nil && !existing.CreatedAt.IsZero() {
		cp.CreatedAt = existing.CreatedAt
	}
	r.workflows[cp.ID] = cp
	r.mu.Unlock()
	return nil
}

func (r *MemoryRepository) GetWorkflow(_ context.Context, workflowID string) (*Workflow, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	workflow := r.workflows[workflowID]
	if workflow == nil {
		return nil, errors.New("workflow not found")
	}
	return cloneWorkflow(workflow), nil
}

func (r *MemoryRepository) ListWorkflows(_ context.Context) ([]*Workflow, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Workflow, 0, len(r.workflows))
	for _, workflow := range r.workflows {
		out = append(out, cloneWorkflow(workflow))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *MemoryRepository) SaveVersion(_ context.Context, version *WorkflowVersion) error {
	if version == nil || version.Definition == nil {
		return errors.New("workflow version is nil")
	}
	if version.ID == "" {
		return errors.New("workflow version id is required")
	}
	if version.WorkflowID == "" {
		return errors.New("workflow id is required")
	}

	now := time.Now()
	cp := cloneVersion(version)
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = now
	}
	if cp.Status == "" {
		cp.Status = VersionDraft
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.versions[cp.ID]; existing != nil && !existing.CreatedAt.IsZero() {
		cp.CreatedAt = existing.CreatedAt
	}
	r.versions[cp.ID] = cp
	r.byFlow[cp.WorkflowID] = appendUniqueVersionID(r.byFlow[cp.WorkflowID], cp.ID)
	if workflow := r.workflows[cp.WorkflowID]; workflow == nil {
		r.workflows[cp.WorkflowID] = &Workflow{
			ID:        cp.WorkflowID,
			Name:      cp.Definition.Name,
			CreatedAt: now,
			UpdatedAt: now,
		}
	} else {
		workflow.UpdatedAt = now
	}
	return nil
}

func (r *MemoryRepository) GetVersion(_ context.Context, versionID string) (*WorkflowVersion, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	version := r.versions[versionID]
	if version == nil {
		return nil, errors.New("workflow version not found")
	}
	return cloneVersion(version), nil
}

func (r *MemoryRepository) ListVersions(_ context.Context, workflowID string) ([]*WorkflowVersion, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := append([]string{}, r.byFlow[workflowID]...)
	sort.Strings(ids)
	out := make([]*WorkflowVersion, 0, len(ids))
	for _, id := range ids {
		if version := r.versions[id]; version != nil {
			out = append(out, cloneVersion(version))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Version != out[j].Version {
			return out[i].Version < out[j].Version
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *MemoryRepository) PublishVersion(_ context.Context, versionID string) (*WorkflowVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	version := r.versions[versionID]
	if version == nil {
		return nil, errors.New("workflow version not found")
	}
	for _, id := range r.byFlow[version.WorkflowID] {
		if other := r.versions[id]; other != nil && other.ID != versionID && other.Status == VersionPublished {
			other.Status = VersionArchived
		}
	}
	version.Status = VersionPublished
	workflow := r.workflows[version.WorkflowID]
	if workflow == nil {
		return nil, fmt.Errorf("workflow not found for version %s", versionID)
	}
	workflow.ActiveVersion = version.ID
	workflow.UpdatedAt = time.Now()
	return cloneVersion(version), nil
}

func (r *MemoryRepository) GetActiveVersion(_ context.Context, workflowID string) (*WorkflowVersion, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	workflow := r.workflows[workflowID]
	if workflow == nil {
		return nil, errors.New("workflow not found")
	}
	if workflow.ActiveVersion == "" {
		return nil, errors.New("workflow has no active version")
	}
	version := r.versions[workflow.ActiveVersion]
	if version == nil {
		return nil, errors.New("active workflow version not found")
	}
	return cloneVersion(version), nil
}

func appendUniqueVersionID(items []string, versionID string) []string {
	for _, item := range items {
		if item == versionID {
			return items
		}
	}
	return append(items, versionID)
}

func cloneWorkflow(workflow *Workflow) *Workflow {
	if workflow == nil {
		return nil
	}
	cp := *workflow
	cp.Tags = append([]string{}, workflow.Tags...)
	cp.Metadata = cloneAnyMap(workflow.Metadata)
	return &cp
}

func cloneVersion(version *WorkflowVersion) *WorkflowVersion {
	if version == nil {
		return nil
	}
	cp := *version
	cp.Definition = cloneDefinition(version.Definition)
	return &cp
}

func cloneDefinition(def *WorkflowDefinition) *WorkflowDefinition {
	if def == nil {
		return nil
	}
	raw, err := json.Marshal(def)
	if err != nil {
		return &WorkflowDefinition{
			ID:            def.ID,
			Name:          def.Name,
			Description:   def.Description,
			EntryNodes:    append([]string{}, def.EntryNodes...),
			PublishConfig: def.PublishConfig,
			Metadata:      cloneAnyMap(def.Metadata),
		}
	}
	var out WorkflowDefinition
	if err := json.Unmarshal(raw, &out); err != nil {
		return &WorkflowDefinition{
			ID:            def.ID,
			Name:          def.Name,
			Description:   def.Description,
			EntryNodes:    append([]string{}, def.EntryNodes...),
			PublishConfig: def.PublishConfig,
			Metadata:      cloneAnyMap(def.Metadata),
		}
	}
	return &out
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
