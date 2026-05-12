package scheduler

import (
	"encoding/json"
	"sync"
)

// Event represents a real-time event broadcast to SSE clients.
type Event struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// EventBus is a simple pub/sub hub for broadcasting events to SSE clients.
type EventBus struct {
	mu      sync.RWMutex
	clients map[chan Event]struct{}
}

func NewEventBus() *EventBus {
	return &EventBus{
		clients: make(map[chan Event]struct{}),
	}
}

// Subscribe returns a channel that receives broadcast events.
// The caller must call Unsubscribe when done.
func (eb *EventBus) Subscribe() chan Event {
	ch := make(chan Event, 32) // buffered to avoid blocking
	eb.mu.Lock()
	eb.clients[ch] = struct{}{}
	eb.mu.Unlock()
	return ch
}

// Unsubscribe removes a client channel and closes it.
func (eb *EventBus) Unsubscribe(ch chan Event) {
	eb.mu.Lock()
	delete(eb.clients, ch)
	eb.mu.Unlock()
	close(ch)
}

// Publish sends an event to all subscribed clients.
// Non-blocking: drops events for slow clients.
func (eb *EventBus) Publish(evt Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for ch := range eb.clients {
		select {
		case ch <- evt:
		default:
			// Drop event for slow client — buffer is full
		}
	}
}

// ClientCount returns the number of connected SSE clients.
func (eb *EventBus) ClientCount() int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	return len(eb.clients)
}

// FormatSSE formats an Event as an SSE data line.
func FormatSSE(evt Event) ([]byte, error) {
	data, err := json.Marshal(evt)
	if err != nil {
		return nil, err
	}
	// SSE format: "data: {json}\n\n"
	return append(append([]byte("data: "), data...), '\n', '\n'), nil
}
