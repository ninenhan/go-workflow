package fn

import (
	"errors"
	"sync"
	"sync/atomic"
)

// Event holds an ID and data of type T.
type Event[T any] struct {
	ID   int64
	Data T
}

// Subscriber receives events of type T.
type Subscriber[T any] struct {
	ID     int64
	Ch     chan Event[T]
	Cursor int64
}

// Topic manages subscriptions and publishing for type T.
type Topic[T any] struct {
	mu       sync.RWMutex
	name     string
	history  []Event[T]
	subs     map[int64]*Subscriber[T]
	nextID   int64
	subIDGen int64
	maxCache int
	dropped  int64
}

// NewTopic creates a new Topic.
func NewTopic[T any](name string, maxCache int) *Topic[T] {
	return &Topic[T]{
		name:     name,
		history:  make([]Event[T], 0, maxCache),
		subs:     make(map[int64]*Subscriber[T]),
		maxCache: maxCache,
	}
}

// Publish sends data to all subscribers and stores it in history.
func (t *Topic[T]) Publish(data T) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	evt := Event[T]{ID: t.nextID, Data: data}
	t.nextID++
	t.history = append(t.history, evt)
	if len(t.history) > t.maxCache {
		t.history = t.history[1:]
	}
	for _, sub := range t.subs {
		select {
		case sub.Ch <- evt:
		default:
			// Drop on full buffer
			atomic.AddInt64(&t.dropped, 1)
			return errors.New("subscriber buffer full")
		}
	}
	return nil
}

// Subscribe adds a new subscriber with buffer size.
func (t *Topic[T]) Subscribe(buffer int) *Subscriber[T] {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.subIDGen++
	sub := &Subscriber[T]{
		ID: t.subIDGen,
		Ch: make(chan Event[T], buffer),
	}
	// Replay history to new subscriber
	for _, evt := range t.history {
		sub.Ch <- evt
		sub.Cursor = evt.ID
	}
	t.subs[sub.ID] = sub
	return sub
}

// Unsubscribe removes subscriber and closes channel.
func (t *Topic[T]) Unsubscribe(subID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if sub, ok := t.subs[subID]; ok {
		close(sub.Ch)
		delete(t.subs, subID)
	}
}

// Snapshot returns a copy of history.
func (t *Topic[T]) Snapshot() []Event[T] {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cp := make([]Event[T], len(t.history))
	copy(cp, t.history)
	return cp
}

func (t *Topic[T]) DroppedCount() int64 {
	return atomic.LoadInt64(&t.dropped)
}

// EventBus manages multiple topics for type T.
type EventBus[T any] struct {
	mu     sync.RWMutex
	topics map[string]*Topic[T]
}

// NewBus creates a new EventBus.
func NewBus[T any]() *EventBus[T] {
	return &EventBus[T]{
		topics: make(map[string]*Topic[T]),
	}
}

// CreateTopic creates a topic and returns it.
func (b *EventBus[T]) CreateTopic(name string, maxCache int) (*Topic[T], error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.topics[name]; exists {
		return nil, errors.New("topic exists")
	}
	topic := NewTopic[T](name, maxCache)
	b.topics[name] = topic
	return topic, nil
}

func (b *EventBus[T]) GetTopic(name string) *Topic[T] {
	return b.topics[name]
}

// Publish sends data to a named topic.
func (b *EventBus[T]) Publish(topic string, data T) error {
	b.mu.RLock()
	t, ok := b.topics[topic]
	b.mu.RUnlock()
	if !ok {
		return errors.New("topic not found")
	}
	return t.Publish(data)
}

// Subscribe returns a subscriber to a topic.
func (b *EventBus[T]) Subscribe(topic string, buffer int) (*Subscriber[T], error) {
	b.mu.RLock()
	t, ok := b.topics[topic]
	b.mu.RUnlock()
	if !ok {
		return nil, errors.New("topic not found")
	}
	return t.Subscribe(buffer), nil
}

// Example usage:
//
//// type Order struct {
////     ID     int64
////     Amount float64
//// }
////
//// func main() {
////     bus := NewBus[Order]()
////     bus.CreateTopic("orders", 100)
////
////     sub, _ := bus.Subscribe("orders", 10)
////     go func() {
////         for evt := range sub.Ch {
////             fmt.Printf("Received Order: %+v\n", evt.Data)
////         }
////     }()
////
////     bus.Publish("orders", Order{ID: 1, Amount: 99.99})
//// }
