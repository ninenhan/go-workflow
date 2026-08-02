package credential

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	fileStoreVersion     = 1
	fileStoreKeyBytes    = 32
	maxEncryptedFileSize = 16 << 20
)

var fileStoreAdditionalData = []byte("go-workflow-credentials-v1")

type FileStore struct {
	mu       sync.RWMutex
	key      []byte
	dataPath string
	scopes   map[string]map[string]memoryEntry
}

type encryptedEnvelope struct {
	Version    int    `json:"version"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type persistedPayload struct {
	Version int                                       `json:"version"`
	Scopes  map[string]map[string]persistedCredential `json:"scopes"`
}

type persistedCredential struct {
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func OpenFileStore(directory string) (*FileStore, error) {
	directory = filepath.Clean(directory)
	if directory == "." || directory == "" {
		return nil, errors.New("credential store directory is required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create credential store directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("secure credential store directory: %w", err)
	}

	keyPath := filepath.Join(directory, "credentials.key")
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	store := &FileStore{
		key:      key,
		dataPath: filepath.Join(directory, "credentials.enc"),
		scopes:   make(map[string]map[string]memoryEntry),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileStore) PutCredential(ctx context.Context, scope, name, value string) (Metadata, error) {
	metadata, err := s.PutCredentials(ctx, scope, map[string]string{name: value})
	if err != nil {
		return Metadata{}, err
	}
	return metadata[0], nil
}

func (s *FileStore) PutCredentials(_ context.Context, scope string, values map[string]string) ([]Metadata, error) {
	scope, normalized, err := normalizeBatch(scope, values)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneCredentialScopes(s.scopes)
	entries := next[scope]
	if entries == nil {
		entries = make(map[string]memoryEntry)
		next[scope] = entries
	}
	result := make([]Metadata, 0, len(normalized))
	for name, value := range normalized {
		entry, exists := entries[name]
		if !exists {
			entry.metadata = Metadata{Name: name, CreatedAt: now}
		}
		entry.value = value
		entry.metadata.UpdatedAt = now
		entries[name] = entry
		result = append(result, entry.metadata)
	}
	if err := s.persist(next); err != nil {
		return nil, err
	}
	s.scopes = next
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (s *FileStore) ResolveCredential(_ context.Context, scope, name string) (string, error) {
	scope, err := NormalizeScope(scope)
	if err != nil {
		return "", err
	}
	name, err = NormalizeName(name)
	if err != nil {
		return "", err
	}
	s.mu.RLock()
	entry, exists := s.scopes[scope][name]
	s.mu.RUnlock()
	if !exists {
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return entry.value, nil
}

func (s *FileStore) DeleteCredential(_ context.Context, scope, name string) error {
	scope, err := NormalizeScope(scope)
	if err != nil {
		return err
	}
	name, err = NormalizeName(name)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.scopes[scope][name]; !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	next := cloneCredentialScopes(s.scopes)
	delete(next[scope], name)
	if len(next[scope]) == 0 {
		delete(next, scope)
	}
	if err := s.persist(next); err != nil {
		return err
	}
	s.scopes = next
	return nil
}

func (s *FileStore) ListCredentials(_ context.Context, scope string) ([]Metadata, error) {
	scope, err := NormalizeScope(scope)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	result := make([]Metadata, 0, len(s.scopes[scope]))
	for _, entry := range s.scopes[scope] {
		result = append(result, entry.metadata)
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (s *FileStore) load() error {
	info, err := os.Stat(s.dataPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect credential store: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("credential store permissions must not allow group or world access: %s", info.Mode().Perm())
	}
	if info.Size() > maxEncryptedFileSize {
		return fmt.Errorf("credential store exceeds %d bytes", maxEncryptedFileSize)
	}
	encoded, err := os.ReadFile(s.dataPath)
	if err != nil {
		return fmt.Errorf("read credential store: %w", err)
	}
	scopes, err := decryptCredentialScopes(s.key, encoded)
	if err != nil {
		return fmt.Errorf("decrypt credential store: %w", err)
	}
	s.scopes = scopes
	return nil
}

func (s *FileStore) persist(scopes map[string]map[string]memoryEntry) error {
	encoded, err := encryptCredentialScopes(s.key, scopes)
	if err != nil {
		return fmt.Errorf("encrypt credential store: %w", err)
	}
	if len(encoded) > maxEncryptedFileSize {
		return fmt.Errorf("credential store exceeds %d bytes", maxEncryptedFileSize)
	}
	if err := writeAtomicFile(s.dataPath, encoded, 0o600); err != nil {
		return fmt.Errorf("persist credential store: %w", err)
	}
	return nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return nil, fmt.Errorf("inspect credential key: %w", statErr)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("credential key permissions must not allow group or world access: %s", info.Mode().Perm())
		}
		if len(key) != fileStoreKeyBytes {
			return nil, fmt.Errorf("credential key must contain exactly %d bytes", fileStoreKeyBytes)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read credential key: %w", err)
	}
	key = make([]byte, fileStoreKeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate credential key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create credential key: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(key); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write credential key: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("sync credential key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close credential key: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure credential key: %w", err)
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("sync credential key directory: %w", err)
	}
	complete = true
	return key, nil
}

func encryptCredentialScopes(key []byte, scopes map[string]map[string]memoryEntry) ([]byte, error) {
	aead, err := credentialAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	payload := persistedPayload{
		Version: fileStoreVersion,
		Scopes:  make(map[string]map[string]persistedCredential, len(scopes)),
	}
	for scope, entries := range scopes {
		payload.Scopes[scope] = make(map[string]persistedCredential, len(entries))
		for name, entry := range entries {
			payload.Scopes[scope][name] = persistedCredential{
				Value:     entry.value,
				CreatedAt: entry.metadata.CreatedAt,
				UpdatedAt: entry.metadata.UpdatedAt,
			}
		}
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, fileStoreAdditionalData)
	return json.Marshal(encryptedEnvelope{
		Version:    fileStoreVersion,
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	})
}

func decryptCredentialScopes(key, encoded []byte) (map[string]map[string]memoryEntry, error) {
	var envelope encryptedEnvelope
	if err := decodeStrictJSON(encoded, &envelope); err != nil {
		return nil, err
	}
	if envelope.Version != fileStoreVersion {
		return nil, fmt.Errorf("unsupported encrypted credential store version: %d", envelope.Version)
	}
	aead, err := credentialAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != aead.NonceSize() {
		return nil, errors.New("credential store nonce is invalid")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, errors.New("credential store ciphertext is invalid")
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, fileStoreAdditionalData)
	if err != nil {
		return nil, errors.New("credential store authentication failed")
	}
	var payload persistedPayload
	if err := decodeStrictJSON(plaintext, &payload); err != nil {
		return nil, err
	}
	if payload.Version != fileStoreVersion {
		return nil, fmt.Errorf("unsupported credential payload version: %d", payload.Version)
	}
	scopes := make(map[string]map[string]memoryEntry, len(payload.Scopes))
	for scope, entries := range payload.Scopes {
		normalizedScope, err := NormalizeScope(scope)
		if err != nil || normalizedScope != scope {
			return nil, fmt.Errorf("credential store contains invalid scope %q", scope)
		}
		if len(entries) > MaxBatchSize {
			return nil, fmt.Errorf("credential scope %q exceeds %d entries", scope, MaxBatchSize)
		}
		scopes[scope] = make(map[string]memoryEntry, len(entries))
		for name, entry := range entries {
			normalizedName, err := NormalizeName(name)
			if err != nil || normalizedName != name {
				return nil, fmt.Errorf("credential store contains invalid name %q", name)
			}
			if entry.CreatedAt.IsZero() || entry.UpdatedAt.IsZero() || entry.UpdatedAt.Before(entry.CreatedAt) {
				return nil, fmt.Errorf("credential store contains invalid timestamps for %s", name)
			}
			if strings.TrimSpace(entry.Value) == "" || len(entry.Value) > MaxValueBytes {
				return nil, fmt.Errorf("credential store contains invalid value for %s", name)
			}
			scopes[scope][name] = memoryEntry{
				value: entry.Value,
				metadata: Metadata{
					Name:      name,
					CreatedAt: entry.CreatedAt,
					UpdatedAt: entry.UpdatedAt,
				},
			}
		}
	}
	return scopes, nil
}

func credentialAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != fileStoreKeyBytes {
		return nil, fmt.Errorf("credential key must contain exactly %d bytes", fileStoreKeyBytes)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func decodeStrictJSON(source []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON payload must contain one document")
		}
		return err
	}
	return nil
}

func cloneCredentialScopes(source map[string]map[string]memoryEntry) map[string]map[string]memoryEntry {
	result := make(map[string]map[string]memoryEntry, len(source))
	for scope, entries := range source {
		result[scope] = make(map[string]memoryEntry, len(entries))
		for name, entry := range entries {
			result[scope][name] = entry
		}
	}
	return result
}

func writeAtomicFile(path string, content []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".credentials-*.tmp")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func syncDirectory(directory string) error {
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}
