package policy

import (
	"sync"
	"time"
)

// circuitBreaker counts recent AUTO decisions and trips when the count exceeds
// MaxActionsPerWindow within WindowSeconds. Thread-safe.
type circuitBreaker struct {
	mu         sync.Mutex
	timestamps []time.Time
	max        int
	window     time.Duration
}

func newCircuitBreaker(cfg CircuitBreakerConfig) *circuitBreaker {
	max := cfg.MaxActionsPerWindow
	if max <= 0 {
		max = 5
	}
	win := time.Duration(cfg.WindowSeconds) * time.Second
	if win <= 0 {
		win = 5 * time.Minute
	}
	return &circuitBreaker{max: max, window: win}
}

// tripped returns true if the circuit breaker has fired — i.e. the number of
// AUTO decisions within the rolling window is >= max. Does not record.
func (cb *circuitBreaker) tripped() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.evict()
	return len(cb.timestamps) >= cb.max
}

// record adds the current time as an AUTO decision event.
func (cb *circuitBreaker) record() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.timestamps = append(cb.timestamps, time.Now())
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
