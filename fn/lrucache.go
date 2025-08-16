package fn

import (
	"container/list"
	"sync"
)

// LRUCache 是一个并发安全的泛型 LRU 缓存
type LRUCache[K comparable, V any] struct {
	capacity int
	ll       *list.List
	cache    map[K]*list.Element
	mu       sync.Mutex
}

type entry[K comparable, V any] struct {
	key   K
	value V
}

type Option[K comparable, V any] func(*LRUCache[K, V])

// NewLRUCache 新建一个最大容量为 cap 的 LRUCache
func NewLRUCache[K comparable, V any](cap int) *LRUCache[K, V] {
	if cap <= 0 {
		panic("LRUCache capacity must be > 0")
	}
	return &LRUCache[K, V]{
		capacity: cap,
		ll:       list.New(),
		cache:    make(map[K]*list.Element, cap),
	}
}

// Get 取值；存在时把该条目移到队首（最近使用），返回 true；否则返回 false
func (c *LRUCache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ele, ok := c.cache[key]; ok {
		c.ll.MoveToFront(ele)
		return ele.Value.(*entry[K, V]).value, true
	}
	var zero V
	return zero, false
}

// Put 插入或更新：如果已存在，更新值并移到队首；否则插入新条目，超出容量时驱逐最旧的
func (c *LRUCache[K, V]) Put(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ele, ok := c.cache[key]; ok {
		ele.Value.(*entry[K, V]).value = value
		c.ll.MoveToFront(ele)
		return
	}
	// 插入新元素
	ele := c.ll.PushFront(&entry[K, V]{key: key, value: value})
	c.cache[key] = ele

	// 容量超限时删除尾部
	if c.ll.Len() > c.capacity {
		last := c.ll.Back()
		if last != nil {
			c.ll.Remove(last)
			kv := last.Value.(*entry[K, V])
			delete(c.cache, kv.key)
		}
	}
}

// Len 返回当前缓存大小
func (c *LRUCache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// Trash 删除
func (c *LRUCache[K, V]) Trash(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ele, ok := c.cache[key]; ok {
		c.ll.Remove(ele)
		delete(c.cache, key)
	}
}
