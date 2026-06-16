package audit

import (
	"context"
	"sync"
)

// MemorySink holds audit events in-memory. Used in tests and dev mode.
// All operations are goroutine-safe.
type MemorySink struct {
	mu     sync.RWMutex
	events []AuditEvent
}

// Record appends ev to the in-memory log.
func (m *MemorySink) Record(_ context.Context, ev AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, ev)
	return nil
}

// Query returns all events that match filter, in insertion order.
func (m *MemorySink) Query(_ context.Context, f QueryFilter) ([]AuditEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []AuditEvent
	for _, ev := range m.events {
		if f.matches(ev) {
			out = append(out, ev)
			if f.Limit > 0 && len(out) >= f.Limit {
				break
			}
		}
	}
	return out, nil
}

// All returns a snapshot of every event (no filtering). Useful in tests.
func (m *MemorySink) All() []AuditEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]AuditEvent, len(m.events))
	copy(out, m.events)
	return out
}

// compile-time interface assertion
var _ AuditSink = (*MemorySink)(nil)
