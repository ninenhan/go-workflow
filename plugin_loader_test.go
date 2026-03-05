package workflow

import (
	"fmt"
	"runtime"
	"testing"
)

func TestCallPluginRegisterNoArg(t *testing.T) {
	called := false
	err := callPluginRegister(func() error {
		called = true
		return nil
	}, NewRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("expected register func to be called")
	}
}

func TestCallPluginRegisterWithRegistry(t *testing.T) {
	reg := NewRegistry()
	err := callPluginRegister(func(r *Registry) error {
		r.Register("TestUnit", func() ExecutableUnit { return nil })
		return nil
	}, reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := reg.factories["TestUnit"]; !ok {
		t.Fatalf("expected TestUnit to be registered")
	}
}

func TestCallPluginRegisterUnsupportedSignature(t *testing.T) {
	err := callPluginRegister(func(v int) error { return fmt.Errorf("%d", v) }, NewRegistry())
	if err == nil {
		t.Fatalf("expected error for unsupported signature")
	}
}

func TestDefaultPluginDir(t *testing.T) {
	got := DefaultPluginDir("/srv/workflow")
	if got == "" {
		t.Fatalf("default plugin dir is empty")
	}
}

func TestLoadUnitPluginsMissingDir(t *testing.T) {
	loaded, err := LoadUnitPlugins(t.TempDir()+"/missing", nil)
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(loaded) != 0 {
			t.Fatalf("expected no loaded plugins, got %d", len(loaded))
		}
		return
	}
	if err == nil {
		t.Fatalf("expected unsupported platform error")
	}
}
