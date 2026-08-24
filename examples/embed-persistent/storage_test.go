package main

import (
	"context"
	"strings"
	"testing"

	workflow "github.com/ninenhan/go-workflow"
)

func TestOpenWorkflowStoresDefaultsToMemory(t *testing.T) {
	t.Setenv(databaseDriverEnvironment, "")
	t.Setenv(databaseDSNEnvironment, "")
	application, err := openWorkflowApplication(context.Background(), t.TempDir(), workflow.RuntimeOptions{})
	if err != nil {
		t.Fatalf("open memory stores: %v", err)
	}
	defer application.Close()
	if err := application.Stores.Validate(); err != nil {
		t.Fatalf("memory stores are incomplete: %v", err)
	}
	if application.Stores.Automations == nil {
		t.Fatal("memory automation store is missing")
	}
}

func TestOpenWorkflowStoresRequiresMySQLDSN(t *testing.T) {
	t.Setenv(databaseDriverEnvironment, "mysql")
	t.Setenv(databaseDSNEnvironment, "")
	_, err := openWorkflowApplication(context.Background(), t.TempDir(), workflow.RuntimeOptions{})
	if err == nil || !strings.Contains(err.Error(), databaseDSNEnvironment) {
		t.Fatalf("MySQL DSN error = %v", err)
	}
}

func TestOpenWorkflowStoresRejectsUnknownDriver(t *testing.T) {
	t.Setenv(databaseDriverEnvironment, "unknown")
	_, err := openWorkflowApplication(context.Background(), t.TempDir(), workflow.RuntimeOptions{})
	if err == nil || !strings.Contains(err.Error(), "memory, sqlite, or mysql") {
		t.Fatalf("unknown driver error = %v", err)
	}
}
