package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/persist"
	"github.com/ninenhan/go-workflow/scheduler"
	"github.com/ninenhan/go-workflow/worker"
	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

type facadeTestUnit struct{ workerunit.Unit }

func (u *facadeTestUnit) GetUnitName() string { return "FacadeTestUnit" }

func (u *facadeTestUnit) GetUnitMeta() *workerunit.Unit { return &u.Unit }

func (u *facadeTestUnit) Execute(
	context.Context,
	workerunit.ContextMap,
	*workerunit.Node,
) (*workerunit.ExecutionResult, error) {
	return workerunit.SimpleResult("facade-ok"), nil
}

func TestOpenMemoryProvidesCompleteStoresAndCustomUnits(t *testing.T) {
	app, err := OpenMemoryWithOptions(context.Background(), RuntimeOptions{
		ConfigureWorker: func(service *worker.Service) error {
			return service.UnitRegistry().Register("FacadeTestUnit", func() workerunit.ExecutableUnit {
				return &facadeTestUnit{}
			})
		},
	})
	if err != nil {
		t.Fatalf("open memory application: %v", err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Errorf("close application: %v", err)
		}
	})
	if err := app.Stores.Validate(); err != nil {
		t.Fatalf("validate stores: %v", err)
	}
	run, err := app.Scheduler.RunDefinition(context.Background(), &definition.WorkflowDefinition{
		ID:         "facade-memory",
		Name:       "facade memory",
		EntryNodes: []string{"unit"},
		Nodes: []definition.Node{{
			ID:       "unit",
			Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "FacadeTestUnit"},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("run definition: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess || run.Context.NodeResults["unit"] != "facade-ok" {
		t.Fatalf("run result = %#v", run)
	}
}

func TestOpenDefaultCreatesSQLiteORMStore(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "workflow")
	app, err := OpenDefaultWithOptions(context.Background(), DefaultOptions{
		DataDirectory: dataDirectory,
		Runtime:       RuntimeOptions{Builtins: BuiltinStandard},
	})
	if err != nil {
		t.Fatalf("open default application: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close default application: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDirectory, "workflow.db")); err != nil {
		t.Fatalf("default SQLite database: %v", err)
	}
}

func TestNewRejectsIncompleteStoresAndUnknownBuiltins(t *testing.T) {
	_, err := New(context.Background(), Options{Stores: &persist.Stores{}})
	if err == nil || err.Error() != "workflow definition repository is required" {
		t.Fatalf("incomplete stores error = %v", err)
	}
	stores, err := OpenMemory(context.Background())
	if err != nil {
		t.Fatalf("open memory for stores: %v", err)
	}
	if err := stores.Close(); err != nil {
		t.Fatalf("close memory application: %v", err)
	}
	_, err = resolveBuiltinMode("unknown")
	if err == nil {
		t.Fatal("expected unknown builtin mode to fail")
	}
}

func TestApplicationShutdownRequiresContext(t *testing.T) {
	app, err := OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Shutdown(nil); err == nil || err.Error() != "shutdown context is required" {
		t.Fatalf("nil shutdown context error = %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationClosePreservesStoreCloseError(t *testing.T) {
	closeMarker := errors.New("store close failed")
	stores, err := persist.NewStores(
		definition.NewMemoryRepository(),
		definition.NewMemoryWorkspaceRepository(),
		wfruntime.NewMemoryStore(),
		scheduler.NewMemoryAutomationStore(),
		credential.NewMemoryStore(),
		func() error { return closeMarker },
	)
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), Options{Stores: stores})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := app.Close(); !errors.Is(err, closeMarker) {
			t.Fatalf("close error = %v", err)
		}
	}
}
