package units

import (
	"reflect"
	"testing"

	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestImportDoesNotMutateDefaultRegistry(t *testing.T) {
	if _, exists := workerunit.DefaultRegistry.New("TextUnit"); exists {
		t.Fatal("importing units mutated the legacy default registry")
	}
}

func TestBuiltinCatalogMatchesRegisteredNames(t *testing.T) {
	registry := workerunit.NewRegistry()
	if err := RegisterBuiltins(registry); err != nil {
		t.Fatalf("register builtin units: %v", err)
	}
	if !reflect.DeepEqual(registry.Names(), BuiltinNames()) {
		t.Fatalf("registered names = %v, catalog names = %v", registry.Names(), BuiltinNames())
	}
}

func newBuiltinUnitExecutor(t *testing.T) *workerunit.Executor {
	t.Helper()
	registry := workerunit.NewRegistry()
	if err := RegisterBuiltins(registry); err != nil {
		t.Fatalf("register builtin units: %v", err)
	}
	return workerunit.NewExecutor(registry)
}
