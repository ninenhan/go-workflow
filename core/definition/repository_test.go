package definition

import (
	"context"
	"testing"
)

func TestMemoryRepository_GetActiveVersion(t *testing.T) {
	repo := NewMemoryRepository()
	if err := repo.SaveWorkflow(context.Background(), &Workflow{ID: "wf-1", Name: "wf"}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := repo.SaveVersion(context.Background(), &WorkflowVersion{
		ID:         "wf-1:v1",
		WorkflowID: "wf-1",
		Version:    1,
		Status:     VersionDraft,
		Definition: &WorkflowDefinition{ID: "wf-1", Name: "wf"},
	}); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	if err := repo.SaveVersion(context.Background(), &WorkflowVersion{
		ID:         "wf-1:v2",
		WorkflowID: "wf-1",
		Version:    2,
		Status:     VersionDraft,
		Definition: &WorkflowDefinition{ID: "wf-1", Name: "wf"},
	}); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	if _, err := repo.PublishVersion(context.Background(), "wf-1:v2"); err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	version, err := repo.GetActiveVersion(context.Background(), "wf-1")
	if err != nil {
		t.Fatalf("get active version: %v", err)
	}
	if version.ID != "wf-1:v2" {
		t.Fatalf("unexpected active version: %s", version.ID)
	}
}
