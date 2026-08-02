package definition

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGormRepositoryPersistsPublishedVersions(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "workflow.db")
	db, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	repository, err := NewGormRepository(db)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	ctx := context.Background()
	if err := repository.SaveWorkflow(ctx, &Workflow{
		ID:       "persistent",
		Name:     "Persistent Workflow",
		Tags:     []string{"api"},
		Metadata: map[string]any{"owner": "workspace"},
	}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	definition := &WorkflowDefinition{
		ID:   "ignored",
		Name: "Persistent Workflow",
		Nodes: []Node{{
			ID: "start", Name: "Text", Executor: ExecutorSpec{Type: ExecutorTypeUnit, Ref: "TextUnit"},
		}},
	}
	first, err := repository.CreateVersion(ctx, "persistent", definition)
	if err != nil {
		t.Fatalf("create first version: %v", err)
	}
	second, err := repository.CreateVersion(ctx, "persistent", definition)
	if err != nil {
		t.Fatalf("create second version: %v", err)
	}
	if _, err := repository.PublishVersion(ctx, first.ID); err != nil {
		t.Fatalf("publish first version: %v", err)
	}
	if _, err := repository.PublishVersion(ctx, second.ID); err != nil {
		t.Fatalf("publish second version: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close first db: %v", err)
	}
	reopenedDB, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{})
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	reopened, err := NewGormRepository(reopenedDB)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	active, err := reopened.GetActiveVersion(ctx, "persistent")
	if err != nil {
		t.Fatalf("get active version: %v", err)
	}
	if active.ID != second.ID || active.Definition.ID != "persistent" {
		t.Fatalf("active version = %#v", active)
	}
	versions, err := reopened.ListVersions(ctx, "persistent")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 || versions[0].Status != VersionArchived || versions[1].Status != VersionPublished {
		t.Fatalf("persisted versions = %#v", versions)
	}
	workflow, err := reopened.GetWorkflow(ctx, "persistent")
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	if workflow.Name != "Persistent Workflow" || workflow.Metadata["owner"] != "workspace" {
		t.Fatalf("persisted workflow = %#v", workflow)
	}
}

func TestGormRepositoryAllocatesConcurrentVersions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:definition-concurrency?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	repository, err := NewGormRepository(db)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}

	const count = 16
	results := make(chan *WorkflowVersion, count)
	errorsFound := make(chan error, count)
	var group sync.WaitGroup
	for index := 0; index < count; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			version, createErr := repository.CreateVersion(context.Background(), "concurrent", &WorkflowDefinition{
				Name: "Concurrent",
				Nodes: []Node{{
					ID: "start", Name: "Text", Executor: ExecutorSpec{Type: ExecutorTypeUnit, Ref: "TextUnit"},
				}},
			})
			if createErr != nil {
				errorsFound <- createErr
				return
			}
			results <- version
		}()
	}
	group.Wait()
	close(results)
	close(errorsFound)
	for createErr := range errorsFound {
		t.Fatalf("create concurrent version: %v", createErr)
	}
	seen := make(map[int]bool, count)
	for version := range results {
		if seen[version.Version] {
			t.Fatalf("duplicate version number: %d", version.Version)
		}
		seen[version.Version] = true
		if version.ID != fmt.Sprintf("concurrent:v%d", version.Version) {
			t.Fatalf("version id = %q", version.ID)
		}
	}
	if len(seen) != count {
		t.Fatalf("created versions = %d, want %d", len(seen), count)
	}
	for version := 1; version <= count; version++ {
		if !seen[version] {
			t.Fatalf("missing version %d", version)
		}
	}
}

func TestGormRepositoryRejectsCorruptDefinitionJSON(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:definition-corruption?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	repository, err := NewGormRepository(db)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	version, err := repository.CreateVersion(context.Background(), "corrupt", &WorkflowDefinition{Name: "Corrupt"})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if err := db.Model(&workflowVersionRecord{}).
		Where("id = ?", version.ID).
		Update("definition", "{").Error; err != nil {
		t.Fatalf("corrupt definition: %v", err)
	}
	if _, err := repository.GetVersion(context.Background(), version.ID); err == nil {
		t.Fatal("expected corrupt definition to fail")
	}
}

func TestGormRepositoryRejectsVersionIdentityMutation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:definition-identity?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	repository, err := NewGormRepository(db)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	version, err := repository.CreateVersion(context.Background(), "immutable", &WorkflowDefinition{Name: "Immutable"})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	version.WorkflowID = "other"
	version.Definition.ID = "other"
	if err := repository.SaveVersion(context.Background(), version); err == nil {
		t.Fatal("expected identity mutation to fail")
	}
}
