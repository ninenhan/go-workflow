package runner

import (
	"fmt"
	"sync"
)

// ResourcePoolCoordinator owns process-wide resource capacity shared by every
// Workflow Run using the same scheduler instance.
type ResourcePoolCoordinator interface {
	TryAcquire(name string, capacity int) (release func(), acquired bool, err error)
	Changed() <-chan struct{}
}

type resourcePoolState struct {
	capacity int
	inUse    int
}

type MemoryResourcePoolCoordinator struct {
	mu      sync.Mutex
	pools   map[string]*resourcePoolState
	changed chan struct{}
}

func NewMemoryResourcePoolCoordinator() *MemoryResourcePoolCoordinator {
	return &MemoryResourcePoolCoordinator{
		pools:   make(map[string]*resourcePoolState),
		changed: make(chan struct{}),
	}
}

func (c *MemoryResourcePoolCoordinator) TryAcquire(name string, capacity int) (func(), bool, error) {
	if name == "" || capacity < 1 {
		return nil, false, fmt.Errorf("resource pool name and positive capacity are required")
	}
	c.mu.Lock()
	state := c.pools[name]
	if state == nil {
		state = &resourcePoolState{capacity: capacity}
		c.pools[name] = state
	} else if state.capacity != capacity {
		if state.inUse > 0 {
			c.mu.Unlock()
			return nil, false, fmt.Errorf("resource pool %s is active with capacity %d, requested %d", name, state.capacity, capacity)
		}
		state.capacity = capacity
		c.notifyLocked()
	}
	if state.inUse >= state.capacity {
		c.mu.Unlock()
		return nil, false, nil
	}
	state.inUse++
	c.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			if current := c.pools[name]; current != nil && current.inUse > 0 {
				current.inUse--
				c.notifyLocked()
			}
			c.mu.Unlock()
		})
	}, true, nil
}

func (c *MemoryResourcePoolCoordinator) Changed() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.changed
}

func (c *MemoryResourcePoolCoordinator) notifyLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

var _ ResourcePoolCoordinator = (*MemoryResourcePoolCoordinator)(nil)
