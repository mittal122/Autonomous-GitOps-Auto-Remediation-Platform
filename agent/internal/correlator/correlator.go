// Package correlator groups normalized Signal values into Incident objects,
// deduplicates noise, tracks open/closed state, and emits lifecycle events.
//
// Design decisions:
//   - In-memory store only (no DB); incidents are lost on restart. Persistence
//     is a future prompt.
//   - Correlation key = namespace/resource; signals for the same Kubernetes
//     object within CorrelationWindow are grouped. A future prompt can refine
//     this to correlate by owning Deployment/StatefulSet.
//   - Severity only escalates (warning → critical), never de-escalates within
//     an open incident.
//
// TODO (future prompt): persist incidents to a store (PostgreSQL/SQLite).
// TODO (future prompt): refine correlation key to use owner references.
// TODO (future prompt): hand-off open incidents to policy/remediator.
package correlator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/store"
	"github.com/autosre/agent/internal/uid"
)

// ---------------------------------------------------------------------------
// Event types
// ---------------------------------------------------------------------------

// EventKind classifies an incident lifecycle transition.
type EventKind int8

const (
	EventOpened  EventKind = iota // first signal for a new incident
	EventUpdated                  // additional signal added to an existing incident
	EventClosed                   // resolve window elapsed with no new signals
)

func (k EventKind) String() string {
	switch k {
	case EventOpened:
		return "opened"
	case EventUpdated:
		return "updated"
	case EventClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// IncidentEvent is emitted on every incident lifecycle transition.
type IncidentEvent struct {
	Kind     EventKind
	Incident contracts.Incident
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// Config holds the correlator's configurable time windows.
type Config struct {
	// CorrelationWindow: signals for the same resource within this window
	// are grouped into the same incident. Default: 5m.
	CorrelationWindow time.Duration
	// ResolveWindow: an open incident with no new signals for this long is
	// automatically closed. Default: 10m.
	ResolveWindow time.Duration
	// DedupWindow: a signal with the same (resource, reason) as a recently
	// seen signal within this window is dropped. Default: 1m.
	DedupWindow time.Duration
}

// DefaultConfig returns sensible defaults for production use.
func DefaultConfig() Config {
	return Config{
		CorrelationWindow: 5 * time.Minute,
		ResolveWindow:     10 * time.Minute,
		DedupWindow:       1 * time.Minute,
	}
}

// ---------------------------------------------------------------------------
// Internal store entry
// ---------------------------------------------------------------------------

// entry is the internal representation of an incident plus correlator metadata.
type entry struct {
	incident contracts.Incident
	// seenReasons tracks when each (reason) was last emitted for dedup.
	seenReasons map[string]time.Time
}

// ---------------------------------------------------------------------------
// Correlator
// ---------------------------------------------------------------------------

const eventBufSize = 100

// Correlator receives Signals and produces IncidentEvents.
type Correlator struct {
	mu      sync.RWMutex
	entries map[string]*entry // correlationKey → entry
	cfg     Config
	log     *slog.Logger
	events  chan IncidentEvent
	store   store.Store // nil → in-memory only (no persistence)
}

// Option is a functional option for Correlator.
type Option func(*Correlator)

// WithStore injects a persistent store. When set, every incident state change is
// written through to the store and open incidents are loaded on Run startup.
func WithStore(s store.Store) Option {
	return func(c *Correlator) { c.store = s }
}

// New returns a ready-to-use Correlator.
func New(cfg Config, log *slog.Logger, opts ...Option) *Correlator {
	c := &Correlator{
		entries: make(map[string]*entry),
		cfg:     cfg,
		log:     log,
		events:  make(chan IncidentEvent, eventBufSize),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Run reads Signals from the given channel, processes each one, and runs the
// resolver loop until ctx is cancelled. It closes the Events() channel on exit.
// If a store is configured, open incidents are loaded from it before the first
// signal is processed.
func (c *Correlator) Run(ctx context.Context, signals <-chan contracts.Signal) {
	defer close(c.events)
	c.hydrateFromStore(ctx)
	go c.runResolver(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-signals:
			if !ok {
				return
			}
			c.Process(sig)
		}
	}
}

// hydrateFromStore loads open incidents from the store into the in-memory map.
// Called once at startup; safe to call with a nil store.
func (c *Correlator) hydrateFromStore(ctx context.Context) {
	if c.store == nil {
		return
	}
	records, err := c.store.LoadOpenIncidents(ctx)
	if err != nil {
		c.log.Error("correlator: failed to load open incidents from store", "error", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, rec := range records {
		c.entries[rec.CorrelationKey] = &entry{
			incident:    rec.Incident,
			seenReasons: make(map[string]time.Time), // dedup state is not persisted; accept some duplicates after restart
		}
	}
	if len(records) > 0 {
		c.log.Info("correlator: hydrated open incidents from store", "count", len(records))
	}
}

// Process synchronously applies a single Signal to the incident store.
// It is safe to call concurrently and is directly testable without a goroutine.
func (c *Correlator) Process(sig contracts.Signal) {
	key := correlationKey(sig)
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	e, exists := c.entries[key]
	if !exists {
		// First signal for this resource: open a new incident.
		inc := contracts.Incident{
			ID:                uid.New(),
			Signals:           []contracts.Signal{sig},
			AffectedResources: []string{resourceLabel(sig)},
			Severity:          sig.Severity,
			OpenedAt:          now,
			UpdatedAt:         now,
		}
		c.entries[key] = &entry{
			incident:    inc,
			seenReasons: map[string]time.Time{sig.Reason: now},
		}
		c.log.Info("incident opened",
			"id", inc.ID,
			"key", key,
			"reason", sig.Reason,
			"severity", inc.Severity,
		)
		c.upsertStore(inc, key)
		c.emit(IncidentEvent{Kind: EventOpened, Incident: inc})
		return
	}

	// Reopen a previously closed incident if a new signal arrives.
	if !e.incident.ResolvedAt.IsZero() {
		e.incident.ResolvedAt = time.Time{}
		e.incident.OpenedAt = now
		e.incident.UpdatedAt = now
		e.seenReasons = map[string]time.Time{sig.Reason: now}
		e.incident.Signals = []contracts.Signal{sig}
		c.log.Info("incident reopened", "id", e.incident.ID, "key", key)
		c.upsertStore(e.incident, key)
		c.emit(IncidentEvent{Kind: EventOpened, Incident: e.incident})
		return
	}

	// Dedup: drop the signal if the same reason was seen within DedupWindow.
	if lastSeen, ok := e.seenReasons[sig.Reason]; ok {
		if now.Sub(lastSeen) < c.cfg.DedupWindow {
			return
		}
	}

	// Update the existing open incident.
	e.incident.Signals = append(e.incident.Signals, sig)
	e.incident.UpdatedAt = now
	e.seenReasons[sig.Reason] = now

	// Severity only escalates within an open incident.
	if severityRank(sig.Severity) > severityRank(e.incident.Severity) {
		e.incident.Severity = sig.Severity
	}

	// Track distinct affected resources (e.g. multiple pods of the same Deployment).
	if !contains(e.incident.AffectedResources, resourceLabel(sig)) {
		e.incident.AffectedResources = append(e.incident.AffectedResources, resourceLabel(sig))
	}

	c.log.Info("incident updated",
		"id", e.incident.ID,
		"key", key,
		"reason", sig.Reason,
		"total_signals", len(e.incident.Signals),
	)
	c.upsertStore(e.incident, key)
	c.emit(IncidentEvent{Kind: EventUpdated, Incident: e.incident})
}

// ResolveStale closes all open incidents whose UpdatedAt is older than ResolveWindow.
// It is called automatically by the background resolver but is also safe to call
// directly (useful for tests).
func (c *Correlator) ResolveStale() {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	for key, e := range c.entries {
		if !e.incident.ResolvedAt.IsZero() {
			continue // already closed
		}
		if now.Sub(e.incident.UpdatedAt) >= c.cfg.ResolveWindow {
			e.incident.ResolvedAt = now
			duration := e.incident.ResolvedAt.Sub(e.incident.OpenedAt)
			c.log.Info("incident closed",
				"id", e.incident.ID,
				"key", key,
				"duration", duration,
				"total_signals", len(e.incident.Signals),
			)
			c.upsertStore(e.incident, key)
			c.emit(IncidentEvent{Kind: EventClosed, Incident: e.incident})
		}
	}
}

// ListIncidents returns a snapshot of all incidents (open and closed).
// Each Incident is a deep copy; the caller may freely modify the returned slice.
func (c *Correlator) ListIncidents() []contracts.Incident {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]contracts.Incident, 0, len(c.entries))
	for _, e := range c.entries {
		inc := e.incident
		inc.Signals = append([]contracts.Signal(nil), e.incident.Signals...)
		inc.AffectedResources = append([]string(nil), e.incident.AffectedResources...)
		result = append(result, inc)
	}
	return result
}

// Events returns the read-only channel of IncidentEvents. The channel is closed
// when Run() exits.
func (c *Correlator) Events() <-chan IncidentEvent {
	return c.events
}

// IncidentsHandler returns an http.HandlerFunc that serves a JSON snapshot of
// all incidents at GET /incidents. Intended for local inspection during development.
func (c *Correlator) IncidentsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(c.ListIncidents()); err != nil {
			c.log.Error("failed to encode incidents", "error", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Background resolver
// ---------------------------------------------------------------------------

func (c *Correlator) runResolver(ctx context.Context) {
	// Check at half the resolve window so stale incidents are caught promptly.
	tick := c.cfg.ResolveWindow / 2
	if tick < time.Second {
		tick = time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.ResolveStale()
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// correlationKey returns the string used to group signals into the same incident.
// A non-empty namespace produces "namespace/resource"; a cluster-scoped resource
// (e.g. a Node) produces "cluster/resource".
func correlationKey(sig contracts.Signal) string {
	if sig.Namespace != "" {
		return fmt.Sprintf("%s/%s", sig.Namespace, sig.Resource)
	}
	return fmt.Sprintf("cluster/%s", sig.Resource)
}

// resourceLabel returns a display-friendly label for the signal's resource.
func resourceLabel(sig contracts.Signal) string {
	if sig.Namespace != "" {
		return fmt.Sprintf("%s/%s", sig.Namespace, sig.Resource)
	}
	return sig.Resource
}

// severityRank maps severity strings to comparable integers (higher = worse).
func severityRank(s string) int {
	switch s {
	case "critical":
		return 2
	case "warning":
		return 1
	default:
		return 0
	}
}

// contains reports whether slice contains val.
func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// upsertStore persists an incident to the store if one is configured.
// Must be called while holding c.mu (uses a background context to avoid timeout propagation).
func (c *Correlator) upsertStore(inc contracts.Incident, correlationKey string) {
	if c.store == nil {
		return
	}
	if err := c.store.UpsertIncident(context.Background(), inc, correlationKey); err != nil {
		c.log.Error("correlator: failed to persist incident", "id", inc.ID, "error", err)
	}
}

// emit writes an event to the buffered events channel, dropping and logging if full.
// It is always called while holding c.mu.
func (c *Correlator) emit(e IncidentEvent) {
	select {
	case c.events <- e:
	default:
		c.log.Warn("incident events channel full, dropping event",
			"kind", e.Kind.String(),
			"incident_id", e.Incident.ID,
		)
	}
}
