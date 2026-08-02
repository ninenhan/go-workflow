package credential

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMemoryStoreKeepsValuesOutOfMetadata(t *testing.T) {
	store := NewMemoryStore()
	created, err := store.PutCredential(context.Background(), "workspace-1", "API_TOKEN", "very-secret")
	if err != nil {
		t.Fatalf("put credential: %v", err)
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if strings.Contains(string(encoded), "very-secret") {
		t.Fatal("credential value leaked through metadata")
	}

	value, err := store.ResolveCredential(context.Background(), "workspace-1", "API_TOKEN")
	if err != nil {
		t.Fatalf("resolve credential: %v", err)
	}
	if value != "very-secret" {
		t.Fatalf("credential value = %q", value)
	}

	updated, err := store.PutCredential(context.Background(), "workspace-1", "API_TOKEN", "rotated")
	if err != nil {
		t.Fatalf("rotate credential: %v", err)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) || updated.UpdatedAt.Before(created.UpdatedAt) {
		t.Fatalf("unexpected rotation metadata: created=%v updated=%v", updated.CreatedAt, updated.UpdatedAt)
	}
}

func TestMemoryStoreIsolatesScopesAndDeletesExactly(t *testing.T) {
	store := NewMemoryStore()
	for scope, value := range map[string]string{"workspace-1": "one", "workspace-2": "two"} {
		if _, err := store.PutCredential(context.Background(), scope, "API_TOKEN", value); err != nil {
			t.Fatalf("put %s: %v", scope, err)
		}
	}
	if err := store.DeleteCredential(context.Background(), "workspace-1", "API_TOKEN"); err != nil {
		t.Fatalf("delete credential: %v", err)
	}
	if _, err := store.ResolveCredential(context.Background(), "workspace-1", "API_TOKEN"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted credential error = %v", err)
	}
	value, err := store.ResolveCredential(context.Background(), "workspace-2", "API_TOKEN")
	if err != nil || value != "two" {
		t.Fatalf("other scope credential = %q, %v", value, err)
	}
}

func TestMemoryStoreBatchWriteIsAtomic(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.PutCredentials(context.Background(), "workspace", map[string]string{
		"FIRST_TOKEN":  "first",
		"SECOND_TOKEN": "second",
	}); err != nil {
		t.Fatalf("put credentials: %v", err)
	}
	if _, err := store.PutCredentials(context.Background(), "workspace", map[string]string{
		"FIRST_TOKEN": "rotated",
		"1_INVALID":   "invalid",
	}); err == nil {
		t.Fatal("expected invalid batch to fail")
	}
	value, err := store.ResolveCredential(context.Background(), "workspace", "FIRST_TOKEN")
	if err != nil || value != "first" {
		t.Fatalf("failed batch modified existing credential: %q, %v", value, err)
	}
}

func TestExecutionResolverDoesNotFallBackToProcessEnvironment(t *testing.T) {
	t.Setenv("API_TOKEN", "process-value")
	store := NewMemoryStore()
	ctx, err := WithResolver(context.Background(), store, "workspace-1")
	if err != nil {
		t.Fatalf("attach resolver: %v", err)
	}
	if _, err := Resolve(ctx, "API_TOKEN"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("execution resolver unexpectedly fell back: %v", err)
	}

	value, err := Resolve(context.Background(), "API_TOKEN")
	if err != nil || value != "process-value" {
		t.Fatalf("default environment resolver = %q, %v", value, err)
	}
}

func TestCredentialValidationIsStrict(t *testing.T) {
	store := NewMemoryStore()
	for _, test := range []struct {
		scope string
		name  string
		value string
	}{
		{scope: "", name: "API_TOKEN", value: "secret"},
		{scope: "bad scope", name: "API_TOKEN", value: "secret"},
		{scope: "workspace", name: "1_BAD", value: "secret"},
		{scope: "workspace", name: "API_TOKEN", value: " "},
	} {
		if _, err := store.PutCredential(context.Background(), test.scope, test.name, test.value); err == nil {
			t.Fatalf("expected invalid credential to fail: %#v", test)
		}
	}
}
