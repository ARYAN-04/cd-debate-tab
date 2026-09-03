// Package stream is the non-blocking SSE Hub.
package stream

import (
	"context"
	"strings"
	"sync"
)

// clientBuffer is the per-client channel capacity; slow clients beyond
// this are dropped (PLAN §4D).
const clientBuffer = 4

// Hub fans out one-line SSE frames; slow clients (buffer 4) are dropped.
type Hub struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}

// New returns an empty Hub.
func New() *Hub {
	return &Hub{clients: make(map[chan string]struct{})}
}

// Register adds a buffered (cap 4) client channel to the hub.
func (h *Hub) Register() chan string {
	ch := make(chan string, clientBuffer)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unregister removes a client channel and closes it.
func (h *Hub) Unregister(ch chan string) {
	h.mu.Lock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// Subscribe is an alias for Register.
func (h *Hub) Subscribe() chan string {
	return h.Register()
}

// Unsubscribe is an alias for Unregister.
func (h *Hub) Unsubscribe(ch chan string) {
	h.Unregister(ch)
}

// Count reports the number of registered clients (for tests).
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Run blocks until ctx is cancelled, then reaps all clients so handlers
// watching r.Context().Done() and the hub itself release dead connections.
func (h *Hub) Run(ctx context.Context) error {
	<-ctx.Done()
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		delete(h.clients, ch)
		close(ch)
	}
	return ctx.Err()
}

// Broadcast sends msg non-blocking; slow clients are dropped.
func (h *Hub) Broadcast(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
			delete(h.clients, ch)
			close(ch)
		}
	}
}

// EncodeSSEFrame strips newlines so data: never contains raw \n or \r.
// Each frame carries one data line plus event and retry: 3000 (PLAN §4D).
func EncodeSSEFrame(event, data string) string {
	one := strings.ReplaceAll(data, "\r\n", " ")
	one = strings.ReplaceAll(one, "\r", " ")
	one = strings.ReplaceAll(one, "\n", " ")
	return "event: " + event + "\ndata: " + one + "\nretry: 3000\n\n"
}
