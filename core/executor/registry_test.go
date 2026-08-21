package executor

import (
	"context"
	"errors"
	"testing"
)

type registryTestExecutor struct {
	typeName Type
	marker   string
}

func (e *registryTestExecutor) Type() Type { return e.typeName }

func (e *registryTestExecutor) Execute(context.Context, ExecuteTask) (ExecuteResult, error) {
	return ExecuteResult{Output: e.marker}, nil
}

func TestRegistryRejectsDuplicateWithoutReplacing(t *testing.T) {
	registry := NewRegistry()
	original := &registryTestExecutor{typeName: "custom", marker: "original"}
	if err := registry.Register(original); err != nil {
		t.Fatalf("register original: %v", err)
	}
	if err := registry.Register(&registryTestExecutor{typeName: "custom", marker: "replacement"}); !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("duplicate error = %v", err)
	}
	registered, ok := registry.Get("custom")
	if !ok || registered != original {
		t.Fatalf("duplicate registration replaced original: %#v", registered)
	}
}

func TestRegistryRegisterAllIsAtomic(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&registryTestExecutor{typeName: "existing"}); err != nil {
		t.Fatalf("register existing: %v", err)
	}
	err := registry.RegisterAll(
		&registryTestExecutor{typeName: "new"},
		&registryTestExecutor{typeName: "existing"},
	)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("batch error = %v", err)
	}
	if _, ok := registry.Get("new"); ok {
		t.Fatal("failed batch partially changed registry")
	}
}

func TestRegistryReplacementIsExplicit(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&registryTestExecutor{typeName: "custom", marker: "original"}); err != nil {
		t.Fatalf("register original: %v", err)
	}
	replacement := &registryTestExecutor{typeName: "custom", marker: "replacement"}
	if err := registry.RegisterOrReplace(replacement); err != nil {
		t.Fatalf("replace: %v", err)
	}
	registered, ok := registry.Get("custom")
	if !ok || registered != replacement {
		t.Fatalf("registered executor = %#v", registered)
	}
}

func TestLocalExecutorRejectsDuplicateFunction(t *testing.T) {
	local := NewLocalExecutor()
	fn := func(context.Context, Request) (Result, error) { return Result{}, nil }
	if err := local.Register("custom", fn); err != nil {
		t.Fatalf("register function: %v", err)
	}
	if err := local.Register("custom", fn); !errors.Is(err, ErrLocalFuncAlreadyRegistered) {
		t.Fatalf("duplicate function error = %v", err)
	}
}

func TestRegistriesSupportZeroValues(t *testing.T) {
	var registry Registry
	if err := registry.Register(&registryTestExecutor{typeName: "custom"}); err != nil {
		t.Fatalf("register executor on zero value: %v", err)
	}
	var local LocalExecutor
	if err := local.Register("custom", func(context.Context, Request) (Result, error) { return Result{}, nil }); err != nil {
		t.Fatalf("register function on zero value: %v", err)
	}
}
