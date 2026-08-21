package unit

import (
	"context"
	"errors"
	"testing"
)

type registryTestUnit struct {
	Unit
	marker string
}

func (u *registryTestUnit) GetUnitMeta() *Unit { return &u.Unit }

func (u *registryTestUnit) Execute(context.Context, ContextMap, *Node) (*ExecutionResult, error) {
	return SimpleResult(u.marker), nil
}

func registryTestFactory(marker string) Factory {
	return func() Executable { return &registryTestUnit{marker: marker} }
}

func TestRegistryRejectsDuplicateWithoutReplacing(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("custom", registryTestFactory("original")); err != nil {
		t.Fatalf("register original: %v", err)
	}
	if err := registry.Register("custom", registryTestFactory("replacement")); !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("duplicate error = %v", err)
	}
	created, ok := registry.New("custom")
	if !ok || created.(*registryTestUnit).marker != "original" {
		t.Fatalf("duplicate registration replaced original: %#v", created)
	}
}

func TestRegistryRegisterAllIsAtomic(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("existing", registryTestFactory("existing")); err != nil {
		t.Fatalf("register existing: %v", err)
	}
	err := registry.RegisterAll(
		Registration{Name: "new", Factory: registryTestFactory("new")},
		Registration{Name: "existing", Factory: registryTestFactory("replacement")},
	)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("batch error = %v", err)
	}
	if _, ok := registry.New("new"); ok {
		t.Fatal("failed batch partially changed registry")
	}
}

func TestRegistryReplacementIsExplicit(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("custom", registryTestFactory("original")); err != nil {
		t.Fatalf("register original: %v", err)
	}
	if err := registry.RegisterOrReplace("custom", registryTestFactory("replacement")); err != nil {
		t.Fatalf("replace: %v", err)
	}
	created, ok := registry.New("custom")
	if !ok || created.(*registryTestUnit).marker != "replacement" {
		t.Fatalf("replacement = %#v", created)
	}
}

func TestRegistryCloneDoesNotShareMutations(t *testing.T) {
	original := NewRegistry()
	if err := original.Register("original", registryTestFactory("original")); err != nil {
		t.Fatalf("register original: %v", err)
	}
	cloned := original.Clone()
	if err := cloned.Register("clone-only", registryTestFactory("clone")); err != nil {
		t.Fatalf("register on clone: %v", err)
	}
	if _, ok := original.New("clone-only"); ok {
		t.Fatal("clone mutation leaked into original registry")
	}
}

func TestRegistryRejectsNilFactoryResultAtResolution(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("broken", func() Executable { return nil }); err != nil {
		t.Fatalf("register factory: %v", err)
	}
	if created, ok := registry.New("broken"); ok || created != nil {
		t.Fatalf("broken factory resolved as %#v, %v", created, ok)
	}
}
