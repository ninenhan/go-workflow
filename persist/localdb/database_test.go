package localdb

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/scheduler"
	legacysqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDatabasePersistsDefinitionsAndRuns(t *testing.T) {
	path := filepath.Join(secureTestDirectory(t), "runtime.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	ctx := context.Background()
	version, err := database.Definitions.CreateVersion(ctx, "persistent", &definition.WorkflowDefinition{
		Name: "Persistent",
		Nodes: []definition.Node{{
			ID: "start", Name: "Text", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
		}},
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if _, err := database.Definitions.PublishVersion(ctx, version.ID); err != nil {
		t.Fatalf("publish version: %v", err)
	}
	run := wfruntime.NewWorkflowRun("run-1", "persistent", version.ID, "plan-1")
	run.CredentialScope = "local-workspace"
	if err := database.Runtime.SaveRun(ctx, run); err != nil {
		t.Fatalf("save run: %v", err)
	}
	nextRunAt := time.Date(2026, 7, 25, 1, 30, 0, 0, time.UTC)
	if err := database.Automations.ReconcileSchedules(ctx, []scheduler.AutomationSchedule{{
		Key:          "persistent-automation",
		WorkflowID:   "persistent",
		WorkflowName: "Persistent",
		VersionID:    version.ID,
		TriggerID:    "morning",
		Fingerprint:  "persistent-fingerprint",
		Config: scheduler.AutomationScheduleConfig{
			Frequency: scheduler.AutomationDaily,
			Time:      "09:30",
			Timezone:  "Asia/Taipei",
		},
		Enabled:   true,
		NextRunAt: nextRunAt,
	}}); err != nil {
		t.Fatalf("save automation: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()
	active, err := reopened.Definitions.GetActiveVersion(ctx, "persistent")
	if err != nil || active.ID != version.ID {
		t.Fatalf("active version = %#v, %v", active, err)
	}
	loadedRun, err := reopened.Runtime.LoadRun(ctx, run.ID)
	if err != nil || loadedRun.CredentialScope != "local-workspace" {
		t.Fatalf("loaded run = %#v, %v", loadedRun, err)
	}
	automations, err := reopened.Automations.ListSchedules(ctx)
	if err != nil {
		t.Fatalf("list automations: %v", err)
	}
	if len(automations) != 1 || automations[0].Key != "persistent-automation" ||
		!automations[0].NextRunAt.Equal(nextRunAt) {
		t.Fatalf("loaded automations = %#v", automations)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("database permissions = %s", info.Mode().Perm())
	}
}

func TestDatabaseOpensLegacyCGOSQLiteFile(t *testing.T) {
	path := filepath.Join(secureTestDirectory(t), "legacy.db")
	legacy, err := gorm.Open(legacysqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if err := legacy.Exec(
		"CREATE TABLE migration_probe (value TEXT NOT NULL)",
	).Error; err != nil {
		t.Fatalf("create legacy migration probe: %v", err)
	}
	if err := legacy.Exec(
		"INSERT INTO migration_probe (value) VALUES (?)",
		"legacy data preserved",
	).Error; err != nil {
		t.Fatalf("write legacy migration probe: %v", err)
	}
	legacySQL, err := legacy.DB()
	if err != nil {
		t.Fatalf("access legacy database: %v", err)
	}
	if err := legacySQL.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("secure legacy database: %v", err)
	}

	database, err := Open(path)
	if err != nil {
		t.Fatalf("open legacy database with pure Go driver: %v", err)
	}
	defer database.Close()
	var value string
	if err := database.db.Raw(
		"SELECT value FROM migration_probe LIMIT 1",
	).Scan(&value).Error; err != nil {
		t.Fatalf("read legacy migration probe: %v", err)
	}
	if value != "legacy data preserved" {
		t.Fatalf("legacy migration probe = %q", value)
	}
}

func TestDatabaseRejectsInsecurePermissionsAndCorruption(t *testing.T) {
	if runtime.GOOS != "windows" {
		insecurePath := filepath.Join(secureTestDirectory(t), "insecure.db")
		if err := os.WriteFile(insecurePath, nil, 0o644); err != nil {
			t.Fatalf("create insecure database: %v", err)
		}
		if _, err := Open(insecurePath); err == nil {
			t.Fatal("expected insecure permissions to fail")
		}
	}

	corruptPath := filepath.Join(secureTestDirectory(t), "corrupt.db")
	if err := os.WriteFile(corruptPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("create corrupt database: %v", err)
	}
	if _, err := Open(corruptPath); err == nil {
		t.Fatal("expected corrupt database to fail")
	}

	if runtime.GOOS != "windows" {
		insecureDirectory := filepath.Join(t.TempDir(), "shared")
		if err := os.Mkdir(insecureDirectory, 0o755); err != nil {
			t.Fatalf("create insecure directory: %v", err)
		}
		if _, err := Open(filepath.Join(insecureDirectory, "runtime.db")); err == nil {
			t.Fatal("expected insecure directory permissions to fail")
		}
	}
}

func TestDatabaseFailsInterruptedRunsOnce(t *testing.T) {
	path := filepath.Join(secureTestDirectory(t), "runtime.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	now := wfruntime.NewWorkflowRun("interrupted", "workflow", "workflow:v1", "plan")
	now.Status = wfruntime.StatusRunning
	now.CurrentNodes = []string{"running"}
	now.NodeRuns = map[string]*wfruntime.NodeRun{
		"running": {NodeID: "running", Status: wfruntime.StatusRunning},
		"pending": {NodeID: "pending", Status: wfruntime.StatusPending},
	}
	if err := database.Runtime.SaveRun(context.Background(), now); err != nil {
		t.Fatalf("save interrupted run: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	recovered, err := reopened.Runtime.LoadRun(context.Background(), now.ID)
	if err != nil {
		t.Fatalf("load recovered run: %v", err)
	}
	if recovered.Status != wfruntime.StatusFailed || len(recovered.CurrentNodes) != 0 || recovered.FinishedAt.IsZero() {
		t.Fatalf("recovered run = %#v", recovered)
	}
	if recovered.NodeRuns["running"].Status != wfruntime.StatusFailed ||
		recovered.NodeRuns["running"].Error != wfruntime.InterruptedRunMessage {
		t.Fatalf("running node = %#v", recovered.NodeRuns["running"])
	}
	if recovered.NodeRuns["pending"].Status != wfruntime.StatusCancelled {
		t.Fatalf("pending node = %#v", recovered.NodeRuns["pending"])
	}
	events, err := reopened.Runtime.Events(context.Background(), now.ID)
	if err != nil {
		t.Fatalf("load recovery events: %v", err)
	}
	if len(events) != 1 || events[0].Type != wfruntime.EventRunFinished ||
		events[0].Message != wfruntime.InterruptedRunMessage {
		t.Fatalf("recovery events = %#v", events)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened database: %v", err)
	}

	reopenedAgain, err := Open(path)
	if err != nil {
		t.Fatalf("reopen database again: %v", err)
	}
	defer reopenedAgain.Close()
	events, err = reopenedAgain.Runtime.Events(context.Background(), now.ID)
	if err != nil {
		t.Fatalf("load recovery events again: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("recovery event repeated: %#v", events)
	}
}

func secureTestDirectory(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("create secure test directory: %v", err)
	}
	return directory
}
