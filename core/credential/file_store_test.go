package credential

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStorePersistsEncryptedCredentials(t *testing.T) {
	directory := t.TempDir()
	store, err := OpenFileStore(directory)
	if err != nil {
		t.Fatalf("open file store: %v", err)
	}
	if _, err := store.PutCredentials(context.Background(), "workspace", map[string]string{
		"API_TOKEN":     "secret-token-value",
		"CLIENT_SECRET": "secret-client-value",
	}); err != nil {
		t.Fatalf("put credentials: %v", err)
	}

	encrypted, err := os.ReadFile(filepath.Join(directory, "credentials.enc"))
	if err != nil {
		t.Fatalf("read encrypted store: %v", err)
	}
	for _, secret := range []string{"secret-token-value", "secret-client-value", "API_TOKEN"} {
		if bytes.Contains(encrypted, []byte(secret)) {
			t.Fatalf("encrypted store leaked %q", secret)
		}
	}

	reopened, err := OpenFileStore(directory)
	if err != nil {
		t.Fatalf("reopen file store: %v", err)
	}
	value, err := reopened.ResolveCredential(context.Background(), "workspace", "API_TOKEN")
	if err != nil || value != "secret-token-value" {
		t.Fatalf("reopened credential = %q, %v", value, err)
	}
	metadata, err := reopened.ListCredentials(context.Background(), "workspace")
	if err != nil || len(metadata) != 2 || metadata[0].Name != "API_TOKEN" || metadata[1].Name != "CLIENT_SECRET" {
		t.Fatalf("reopened metadata = %#v, %v", metadata, err)
	}
}

func TestFileStoreRotationAndDeletionSurviveRestart(t *testing.T) {
	directory := t.TempDir()
	store, err := OpenFileStore(directory)
	if err != nil {
		t.Fatalf("open file store: %v", err)
	}
	created, err := store.PutCredential(context.Background(), "workspace", "API_TOKEN", "first")
	if err != nil {
		t.Fatalf("put credential: %v", err)
	}
	rotated, err := store.PutCredential(context.Background(), "workspace", "API_TOKEN", "second")
	if err != nil {
		t.Fatalf("rotate credential: %v", err)
	}
	if !created.CreatedAt.Equal(rotated.CreatedAt) || rotated.UpdatedAt.Before(created.UpdatedAt) {
		t.Fatalf("rotation metadata = %#v -> %#v", created, rotated)
	}
	if err := store.DeleteCredential(context.Background(), "workspace", "API_TOKEN"); err != nil {
		t.Fatalf("delete credential: %v", err)
	}
	reopened, err := OpenFileStore(directory)
	if err != nil {
		t.Fatalf("reopen file store: %v", err)
	}
	if _, err := reopened.ResolveCredential(context.Background(), "workspace", "API_TOKEN"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted credential error = %v", err)
	}
}

func TestFileStoreRejectsTamperingAndInsecureKeyPermissions(t *testing.T) {
	directory := t.TempDir()
	store, err := OpenFileStore(directory)
	if err != nil {
		t.Fatalf("open file store: %v", err)
	}
	if _, err := store.PutCredential(context.Background(), "workspace", "API_TOKEN", "secret"); err != nil {
		t.Fatalf("put credential: %v", err)
	}
	dataPath := filepath.Join(directory, "credentials.enc")
	encrypted, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("read encrypted store: %v", err)
	}
	encrypted[len(encrypted)/2] ^= 1
	if err := os.WriteFile(dataPath, encrypted, 0o600); err != nil {
		t.Fatalf("tamper encrypted store: %v", err)
	}
	if _, err := OpenFileStore(directory); err == nil {
		t.Fatal("expected tampered store to fail")
	}

	cleanDirectory := t.TempDir()
	if _, err := OpenFileStore(cleanDirectory); err != nil {
		t.Fatalf("open clean file store: %v", err)
	}
	keyPath := filepath.Join(cleanDirectory, "credentials.key")
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatalf("make key insecure: %v", err)
	}
	if _, err := OpenFileStore(cleanDirectory); err == nil {
		t.Fatal("expected insecure key permissions to fail")
	}
}
