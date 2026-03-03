package server

import (
	"context"
	"sync"

	workflow "github.com/ninenhan/go-workflow"
)

type EventHub struct {
	mu   sync.RWMutex
	subs map[string]map[chan workflow.Event]struct{}
	buf  int
}

var _ workflow.EventSink = (*EventHub)(nil)

func NewEventHub(buffer int) *EventHub {
	if buffer <= 0 {
		buffer = 1
	}
	return &EventHub{
		subs: make(map[string]map[chan workflow.Event]struct{}),
		buf:  buffer,
	}
}

func (h *EventHub) Emit(ctx context.Context, e workflow.Event) {
	if h == nil || e.RunID == "" {
		return
	}
	h.mu.RLock()
	targets := h.subs[e.RunID]
	h.mu.RUnlock()
	for ch := range targets {
		if ctx.Err() != nil {
			return
		}
		func() {
			defer func() { _ = recover() }()
			select {
			case ch <- e:
			default:
			}
		}()
	}
}

func (h *EventHub) Subscribe(runID string) (<-chan workflow.Event, func()) {
	ch := make(chan workflow.Event, h.buf)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = make(map[string]map[chan workflow.Event]struct{})
	}
	if h.subs[runID] == nil {
		h.subs[runID] = make(map[chan workflow.Event]struct{})
	}
	h.subs[runID][ch] = struct{}{}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		if subs := h.subs[runID]; subs != nil {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(h.subs, runID)
			}
		}
		h.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

func (h *EventHub) CloseRun(runID string) {
	h.mu.Lock()
	subs := h.subs[runID]
	delete(h.subs, runID)
	h.mu.Unlock()
	for ch := range subs {
		close(ch)
	}
}
