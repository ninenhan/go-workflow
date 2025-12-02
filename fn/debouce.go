package fn

import (
	"sync"
	"time"
)

type DebounceManager struct {
	delay time.Duration
	mu    sync.Mutex
	tasks map[uint64]*time.Timer
}

func NewDebounceManager(delay time.Duration) *DebounceManager {
	return &DebounceManager{
		delay: delay,
		tasks: make(map[uint64]*time.Timer),
	}
}

func (m *DebounceManager) Debounce(taskID uint64, fn func(uint64)) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 如果已有定时器，停止重置
	if t, ok := m.tasks[taskID]; ok {
		t.Stop()
	}

	// 创建新的定时器，只执行最后一次
	timer := time.AfterFunc(m.delay, func() {
		fn(taskID)

		// 执行完删除，避免内存泄漏
		m.mu.Lock()
		delete(m.tasks, taskID)
		m.mu.Unlock()
	})

	m.tasks[taskID] = timer
}
