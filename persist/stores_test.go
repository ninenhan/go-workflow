package persist

import (
	"errors"
	"testing"

	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/scheduler"
)

func TestStoresValidatesEveryComponentAndClosesOnce(t *testing.T) {
	closeCalls := 0
	stores, err := NewStores(
		definition.NewMemoryRepository(),
		definition.NewMemoryWorkspaceRepository(),
		wfruntime.NewMemoryStore(),
		scheduler.NewMemoryAutomationStore(),
		credential.NewMemoryStore(),
		func() error {
			closeCalls++
			return errors.New("close marker")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := stores.Close(); err == nil || err.Error() != "close marker" {
			t.Fatalf("close error = %v", err)
		}
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
}
