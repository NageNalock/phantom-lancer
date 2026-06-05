package events

import (
	"context"
	"sync"
)

type Event struct {
	ID        string         `json:"id"`
	Scope     string         `json:"scope"`
	ScopeID   string         `json:"scopeId"`
	Sequence  int64          `json:"sequence"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	CreatedAt string         `json:"createdAt"`
}

type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan Event]struct{})}
}

func (h *Hub) Subscribe(ctx context.Context, scope, scopeID string) <-chan Event {
	key := scope + ":" + scopeID
	ch := make(chan Event, 32)

	h.mu.Lock()
	if h.subs[key] == nil {
		h.subs[key] = make(map[chan Event]struct{})
	}
	h.subs[key][ch] = struct{}{}
	h.mu.Unlock()

	go func() {
		<-ctx.Done()
		h.mu.Lock()
		delete(h.subs[key], ch)
		if len(h.subs[key]) == 0 {
			delete(h.subs, key)
		}
		close(ch)
		h.mu.Unlock()
	}()

	return ch
}

func (h *Hub) Publish(event Event) {
	key := event.Scope + ":" + event.ScopeID

	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[key] {
		select {
		case ch <- event:
		default:
		}
	}
}
