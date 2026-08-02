package credential

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("credential not found")

const (
	MaxBatchSize  = 64
	MaxValueBytes = 64 << 10
)

type Metadata struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Resolver interface {
	ResolveCredential(ctx context.Context, scope, name string) (string, error)
}

type Store interface {
	Resolver
	PutCredential(ctx context.Context, scope, name, value string) (Metadata, error)
	PutCredentials(ctx context.Context, scope string, values map[string]string) ([]Metadata, error)
	DeleteCredential(ctx context.Context, scope, name string) error
	ListCredentials(ctx context.Context, scope string) ([]Metadata, error)
}

type memoryEntry struct {
	value    string
	metadata Metadata
}

type MemoryStore struct {
	mu     sync.RWMutex
	scopes map[string]map[string]memoryEntry
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{scopes: make(map[string]map[string]memoryEntry)}
}

func (s *MemoryStore) PutCredential(ctx context.Context, scope, name, value string) (Metadata, error) {
	metadata, err := s.PutCredentials(ctx, scope, map[string]string{name: value})
	if err != nil {
		return Metadata{}, err
	}
	return metadata[0], nil
}

func (s *MemoryStore) PutCredentials(_ context.Context, scope string, values map[string]string) ([]Metadata, error) {
	scope, normalized, err := normalizeBatch(scope, values)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scopes == nil {
		s.scopes = make(map[string]map[string]memoryEntry)
	}
	entries := s.scopes[scope]
	if entries == nil {
		entries = make(map[string]memoryEntry)
		s.scopes[scope] = entries
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
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func normalizeBatch(scope string, values map[string]string) (string, map[string]string, error) {
	scope, err := NormalizeScope(scope)
	if err != nil {
		return "", nil, err
	}
	if len(values) == 0 {
		return "", nil, errors.New("at least one credential is required")
	}
	if len(values) > MaxBatchSize {
		return "", nil, fmt.Errorf("credential batch must not exceed %d entries", MaxBatchSize)
	}
	normalized := make(map[string]string, len(values))
	for name, value := range values {
		normalizedName, err := NormalizeName(name)
		if err != nil {
			return "", nil, err
		}
		if strings.TrimSpace(value) == "" {
			return "", nil, fmt.Errorf("credential value is required: %s", normalizedName)
		}
		if len(value) > MaxValueBytes {
			return "", nil, fmt.Errorf("credential value must not exceed %d bytes: %s", MaxValueBytes, normalizedName)
		}
		if _, exists := normalized[normalizedName]; exists {
			return "", nil, fmt.Errorf("duplicate credential name: %s", normalizedName)
		}
		normalized[normalizedName] = value
	}
	return scope, normalized, nil
}

func (s *MemoryStore) ResolveCredential(_ context.Context, scope, name string) (string, error) {
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

func (s *MemoryStore) DeleteCredential(_ context.Context, scope, name string) error {
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
	entries := s.scopes[scope]
	if _, exists := entries[name]; !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	delete(entries, name)
	if len(entries) == 0 {
		delete(s.scopes, scope)
	}
	return nil
}

func (s *MemoryStore) ListCredentials(_ context.Context, scope string) ([]Metadata, error) {
	scope, err := NormalizeScope(scope)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	entries := s.scopes[scope]
	result := make([]Metadata, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.metadata)
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

type EnvironmentResolver struct{}

func (EnvironmentResolver) ResolveCredential(_ context.Context, _ string, name string) (string, error) {
	name, err := NormalizeName(name)
	if err != nil {
		return "", err
	}
	value, exists := os.LookupEnv(name)
	if !exists || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return value, nil
}

type executionCredentials struct {
	resolver Resolver
	scope    string
}

type executionCredentialsKey struct{}

func WithResolver(ctx context.Context, resolver Resolver, scope string) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if resolver == nil {
		return nil, errors.New("credential resolver is required")
	}
	normalizedScope := strings.TrimSpace(scope)
	if normalizedScope != "" {
		var err error
		normalizedScope, err = NormalizeScope(normalizedScope)
		if err != nil {
			return nil, err
		}
	}
	return context.WithValue(ctx, executionCredentialsKey{}, executionCredentials{
		resolver: resolver,
		scope:    normalizedScope,
	}), nil
}

func Resolve(ctx context.Context, name string) (string, error) {
	if ctx != nil {
		if execution, ok := ctx.Value(executionCredentialsKey{}).(executionCredentials); ok {
			return execution.resolver.ResolveCredential(ctx, execution.scope, name)
		}
	}
	return EnvironmentResolver{}.ResolveCredential(ctx, "", name)
}

func NormalizeScope(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("credential scope is required")
	}
	if len(value) > 128 {
		return "", errors.New("credential scope must not exceed 128 characters")
	}
	for _, char := range value {
		if char == '-' || char == '_' || char == '.' || char == ':' ||
			char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' ||
			char >= '0' && char <= '9' {
			continue
		}
		return "", fmt.Errorf("credential scope contains invalid character %q", char)
	}
	return value, nil
}

func NormalizeName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("credential name is required")
	}
	if len(value) > 128 {
		return "", errors.New("credential name must not exceed 128 characters")
	}
	for index, char := range value {
		if char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' ||
			index > 0 && char >= '0' && char <= '9' {
			continue
		}
		return "", fmt.Errorf("credential name %q is not a valid environment variable name", value)
	}
	return value, nil
}
