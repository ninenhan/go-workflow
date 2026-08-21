package definition

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func testWorkspace() *Workspace {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	return &Workspace{
		SchemaVersion: WorkspaceSchemaVersion,
		ActiveID:      "wf-one",
		Folders:       []WorkspaceFolder{},
		Entries: []WorkspaceEntry{{
			ID:      "wf-one",
			SavedAt: now,
			Document: WorkspaceDocument{Definition: &WorkflowDefinition{
				ID: "wf-one", Name: "One", Nodes: []Node{},
			}},
		}},
		ServiceActions: []map[string]any{},
	}
}

func TestMemoryWorkspaceRepositoryUsesCompareAndSwapRevision(t *testing.T) {
	repository := NewMemoryWorkspaceRepository()
	created, err := repository.SaveWorkspace(context.Background(), testWorkspace(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.UpdatedAt.IsZero() {
		t.Fatalf("unexpected created workspace: %#v", created)
	}
	created.Entries[0].Document.Definition.Name = "Changed outside repository"
	stored, err := repository.GetWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Entries[0].Document.Definition.Name != "One" {
		t.Fatal("repository returned mutable workspace state")
	}
	if _, err := repository.SaveWorkspace(context.Background(), testWorkspace(), 0); !errors.Is(err, ErrWorkspaceConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}
}

func TestGormWorkspaceRepositoryPersistsAndRejectsStaleWrites(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "workspace.db")
	db, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewGormWorkspaceRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.SaveWorkspace(context.Background(), testWorkspace(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SaveWorkspace(context.Background(), testWorkspace(), 0); !errors.Is(err, ErrWorkspaceConflict) {
		t.Fatalf("expected create conflict, got %v", err)
	}
	created.Entries[0].Document.Definition.Name = "Two"
	updated, err := repository.SaveWorkspace(context.Background(), created, created.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("expected revision 2, got %d", updated.Revision)
	}
	reopenedDB, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewGormWorkspaceRepository(reopenedDB)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := reopened.GetWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 2 || stored.Entries[0].Document.Definition.Name != "Two" {
		t.Fatalf("unexpected reopened workspace: %#v", stored)
	}
}

func TestValidateWorkspaceRejectsBrokenOwnership(t *testing.T) {
	workspace := testWorkspace()
	workspace.ActiveID = "missing"
	if err := ValidateWorkspace(workspace); err == nil {
		t.Fatal("expected missing active shortcut to fail")
	}
	workspace = testWorkspace()
	workspace.Entries[0].Document.Definition.ID = "different"
	if err := ValidateWorkspace(workspace); err == nil {
		t.Fatal("expected mismatched definition identity to fail")
	}
}

func TestWorkflowDefinitionJSONSchemaIsServerGeneratedAndStrict(t *testing.T) {
	schema := WorkflowDefinitionJSONSchema()
	if schema["x-contract-version"] != WorkflowContractVersion {
		t.Fatalf("unexpected contract version: %#v", schema)
	}
	definitions, ok := schema["$defs"].(map[string]any)
	if !ok || definitions["WorkflowDefinition"] == nil || definitions["Node"] == nil {
		t.Fatalf("schema is missing server domain definitions: %#v", definitions)
	}
}

func TestWorkspaceJSONOmitsAbsentNodeRetryPolicy(t *testing.T) {
	workspace := testWorkspace()
	workspace.Entries[0].Document.Definition.Nodes = []Node{{
		ID: "echo", Name: "Echo", Executor: ExecutorSpec{Type: ExecutorTypeLocalGo, Ref: "echo"},
	}}
	raw, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"retry"`) {
		t.Fatalf("absent retry policy must not cross the workspace contract: %s", raw)
	}
}
