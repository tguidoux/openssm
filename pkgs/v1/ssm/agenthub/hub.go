// Package agenthub wakes long-polling agents when new work (commands,
// cancellations, sessions) is queued for their managed instance.
package agenthub

import "sync"

// Hub broadcasts per-instance wake-ups. Callers Subscribe before checking
// for work, then wait on the returned channel, so a Notify that happens
// between the check and the wait is never lost.
type Hub struct {
	mu      sync.Mutex
	waiters map[string]chan struct{}
}

// New creates an empty hub.
func New() *Hub {
	return &Hub{waiters: make(map[string]chan struct{})}
}

// Subscribe returns a channel that is closed on the next Notify for instanceID.
func (h *Hub) Subscribe(instanceID string) <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch, ok := h.waiters[instanceID]
	if !ok {
		ch = make(chan struct{})
		h.waiters[instanceID] = ch
	}
	return ch
}

// Notify wakes every subscriber waiting on instanceID.
func (h *Hub) Notify(instanceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.waiters[instanceID]; ok {
		close(ch)
		delete(h.waiters, instanceID)
	}
}
