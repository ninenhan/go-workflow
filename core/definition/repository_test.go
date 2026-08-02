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

func TestMemoryRepository_CreateVersionAllocatesMonotonicVersions(t *testing.T) {
	repo := NewMemoryRepository()
	workflowDefinition := &WorkflowDefinition{ID: "ignored", Name: "versioned", Nodes: []Node{{
		ID: "n1", Name: "log", Executor: ExecutorSpec{Type: ExecutorTypeUnit, Ref: "LogUnit"},
	}}}

	first, err := repo.CreateVersion(context.Background(), "wf-versioned", workflowDefinition)
	if err != nil {
		t.Fatalf("create first version: %v", err)
	}
	second, err := repo.CreateVersion(context.Background(), "wf-versioned", workflowDefinition)
	if err != nil {
		t.Fatalf("create second version: %v", err)
	}
	if first.ID != "wf-versioned:v1" || first.Version != 1 {
		t.Fatalf("unexpected first version: %+v", first)
	}
	if second.ID != "wf-versioned:v2" || second.Version != 2 {
		t.Fatalf("unexpected second version: %+v", second)
	}
	if first.Definition.ID != "wf-versioned" || workflowDefinition.ID != "ignored" {
		t.Fatalf("definition id was not isolated: stored=%q source=%q", first.Definition.ID, workflowDefinition.ID)
	}
}
