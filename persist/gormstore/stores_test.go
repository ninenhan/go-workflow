package gormstore

import (
	"context"
	"testing"

	"github.com/ninenhan/go-workflow/core/credential"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRecoveryModeMustBeExplicitForInterruptedRuns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:gormstore-recovery?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	disabled, err := New(ctx, db, Options{Credentials: credential.NewMemoryStore()})
	if err != nil {
		t.Fatal(err)
	}
	run := wfruntime.NewWorkflowRun("active", "workflow", "workflow:v1", "plan")
	run.Status = wfruntime.StatusRunning
	if err := disabled.Runtime.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := New(ctx, db, Options{
		Credentials: credential.NewMemoryStore(),
		Recovery:    RecoveryDisabled,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := disabled.Runtime.LoadRun(ctx, run.ID)
	if err != nil || loaded.Status != wfruntime.StatusRunning {
		t.Fatalf("disabled recovery changed run: %#v, %v", loaded, err)
	}
	if _, err := New(ctx, db, Options{
		Credentials: credential.NewMemoryStore(),
		Recovery:    RecoveryFailInterrupted,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err = disabled.Runtime.LoadRun(ctx, run.ID)
	if err != nil || loaded.Status != wfruntime.StatusFailed {
		t.Fatalf("explicit recovery result: %#v, %v", loaded, err)
	}
}

func TestNewRejectsMissingCredentialsAndUnknownRecovery(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:gormstore-options?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), db, Options{}); err == nil {
		t.Fatal("expected missing credentials to fail")
	}
	if _, err := New(context.Background(), db, Options{
		Credentials: credential.NewMemoryStore(),
		Recovery:    "unknown",
	}); err == nil {
		t.Fatal("expected unknown recovery mode to fail")
	}
}
