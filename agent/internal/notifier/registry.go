package notifier

import (
	"sync"
	"time"

	"github.com/autosre/agent/internal/contracts"
)

// PendingApproval is an exported summary of an in-flight approval request.
// Used by the Web UI API to list pending approvals.
type PendingApproval struct {
	RequestID   string                         `json:"request_id"`
	Proposal    contracts.RemediationProposal  `json:"proposal"`
	RequestedAt time.Time                      `json:"requested_at"`
	Deadline    time.Time                      `json:"deadline"`
}

// pendingEntry is one in-flight approval request waiting for a human response.
type pendingEntry struct {
	ch          chan contracts.ApprovalResult
	deadline    time.Time
	requestedAt time.Time
	proposal    contracts.RemediationProposal // stored for Web UI listing
}

// registry is a thread-safe store of pending approval requests.
// Each request is keyed by its unique request ID and holds a channel
// that the inbound Slack handler resolves when the human responds.
type registry struct {
	mu      sync.Mutex
	entries map[string]*pendingEntry
}

func newRegistry() *registry {
	return &registry{entries: make(map[string]*pendingEntry)}
}

// register creates a pending entry for requestID with the given timeout.
// Returns a receive-only channel that is closed or written when resolved.
// Caller must call remove() in a defer to clean up.
func (r *registry) register(id string, proposal contracts.RemediationProposal, timeout time.Duration) <-chan contracts.ApprovalResult {
	ch := make(chan contracts.ApprovalResult, 1)
	now := time.Now()
	r.mu.Lock()
	r.entries[id] = &pendingEntry{
		ch:          ch,
		deadline:    now.Add(timeout),
		requestedAt: now,
		proposal:    proposal,
	}
	r.mu.Unlock()
	return ch
}

// resolve delivers result to the pending entry for id.
// Returns false if id is unknown or already resolved.
func (r *registry) resolve(id string, result contracts.ApprovalResult) bool {
	r.mu.Lock()
	e, ok := r.entries[id]
	if !ok {
		r.mu.Unlock()
		return false
	}
	delete(r.entries, id)
	r.mu.Unlock()

	select {
	case e.ch <- result:
	default:
		// Channel already has a value; drop (idempotent resolve).
	}
	return true
}

// remove deletes the entry without delivering a result.
// Safe to call even if the entry was already resolved.
func (r *registry) remove(id string) {
	r.mu.Lock()
	delete(r.entries, id)
	r.mu.Unlock()
}

// isExpired reports whether the pending request for id is past its deadline.
func (r *registry) isExpired(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return true
	}
	return time.Now().After(e.deadline)
}

// list returns all currently pending (non-expired) approval requests.
func (r *registry) list() []PendingApproval {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	var out []PendingApproval
	for id, e := range r.entries {
		if !now.After(e.deadline) {
			out = append(out, PendingApproval{
				RequestID:   id,
				Proposal:    e.proposal,
				RequestedAt: e.requestedAt,
				Deadline:    e.deadline,
			})
		}
	}
	return out
}
