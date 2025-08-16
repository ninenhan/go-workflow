package fn

import "sync"

type SafeHashSet[T comparable] struct {
	mu   sync.RWMutex
	data map[T]struct{}
}

func NewSafeHashSet[T comparable]() *SafeHashSet[T] {
	return &SafeHashSet[T]{data: make(map[T]struct{})}
}

func (s *SafeHashSet[T]) Add(val T) {
	s.mu.Lock()
	s.data[val] = struct{}{}
	s.mu.Unlock()
}

func (s *SafeHashSet[T]) Remove(val T) {
	s.mu.Lock()
	delete(s.data, val)
	s.mu.Unlock()
}

func (s *SafeHashSet[T]) Has(val T) bool {
	s.mu.RLock()
	_, ok := s.data[val]
	s.mu.RUnlock()
	return ok
}

func (s *SafeHashSet[T]) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

func (s *SafeHashSet[T]) Values() []T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	values := make([]T, 0, len(s.data))
	for k := range s.data {
		values = append(values, k)
	}
	return values
}
