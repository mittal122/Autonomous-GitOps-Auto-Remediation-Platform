package policy

import (
	"context"
	"sync"
	"time"

	"github.com/autosre/agent/internal/store"
)

// circuitBreaker counts recent AUTO decisions and trips when the count exceeds
// MaxActionsPerWindow within WindowSeconds. Thread-safe.
type circuitBreaker struct {
	mu         sync.Mutex
	timestamps []time.Time
	max        int
	window     time.Duration
	store      store.Store // nil → in-memory only
}

func newCircuitBreaker(cfg CircuitBreakerConfig, s store.Store) *circuitBreaker {
	max := cfg.MaxActionsPerWindow
	if max <= 0 {
		max = 5
	}
	win := time.Duration(cfg.WindowSeconds) * time.Second
	if win <= 0 {
		win = 5 * time.Minute
	}
	return &circuitBreaker{max: max, window: win, store: s}
}

// hydrate loads recent CB events from the store into the in-memory slice.
// Called once at construction when a store is provided.
func (cb *circuitBreaker) hydrate() {
	since := time.Now().Add(-cb.window)
	timestamps, err := cb.store.LoadCBEvents(context.Background(), since)
	if err != nil {
		return // non-fatal; circuit breaker starts empty
	}
	cb.mu.Lock()
	cb.timestamps = append(cb.timestamps, timestamps...)
	cb.mu.Unlock()
}

// tripped returns true if the circuit breaker has fired — i.e. the number of
// AUTO decisions within the rolling window is >= max. Does not record.
func (cb *circuitBreaker) tripped() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.evict()
	return len(cb.timestamps) >= cb.max
}

// record adds the current time as an AUTO decision event and persists it if a
// store is configured.
func (cb *circuitBreaker) record() {
	cb.mu.Lock()
	cb.timestamps = append(cb.timestamps, time.Now())
	cb.mu.Unlock()
	if cb.store != nil {
		_ = cb.store.RecordCBEvent(context.Background())
	}
}

// evict removes timestamps that have fallen outside the rolling window.
// Must be called with mu held.
func (cb *circuitBreaker) evict() {
	cutoff := time.Now().Add(-cb.window)
	i := 0
	for i < len(cb.timestamps) && cb.timestamps[i].Before(cutoff) {
		i++
	}
	cb.timestamps = cb.timestamps[i:]
}

// count returns the number of AUTO decisions in the current window (for diagnostics).
func (cb *circuitBreaker) count() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.evict()
	return len(cb.timestamps)
}
