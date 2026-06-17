// Package store defines the persistence layer for the AutoSRE agent.
// The SQLiteStore implementation provides a local, file-backed store with
// WAL-mode concurrency (multiple readers, single writer).
package store

import (
	"context"
	"time"

	"github.com/autosre/agent/internal/contracts"
)

// Store is the persistence layer interface for AutoSRE.
// All methods must be safe for concurrent use.
type Store interface {
	// UpsertIncident writes or updates an incident record keyed by inc.ID.
	UpsertIncident(ctx context.Context, inc contracts.Incident, correlationKey string) error
	// LoadOpenIncidents returns all incidents whose resolved_at is zero.
	LoadOpenIncidents(ctx context.Context) ([]IncidentRecord, error)

	// UpsertApproval persists an in-flight approval request.
	UpsertApproval(ctx context.Context, rec ApprovalRecord) error
	// DeleteApproval removes the approval record for requestID.
	DeleteApproval(ctx context.Context, requestID string) error
	// LoadPendingApprovals returns approvals that have not yet expired.
	LoadPendingApprovals(ctx context.Context) ([]ApprovalRecord, error)

	// RecordCBEvent appends a circuit-breaker AUTO event at the current time.
	RecordCBEvent(ctx context.Context) error
	// LoadCBEvents returns the timestamps of events recorded after `since`.
	LoadCBEvents(ctx context.Context, since time.Time) ([]time.Time, error)

	Close() error
}

// IncidentRecord pairs an Incident with its correlator lookup key.
type IncidentRecord struct {
	Incident       contracts.Incident
	CorrelationKey string
}

// ApprovalRecord is a snapshot of an in-flight approval request for storage.
type ApprovalRecord struct {
	RequestID   string
	IncidentID  string
	Proposal    contracts.RemediationProposal
	RequestedAt time.Time
	ExpiresAt   time.Time
}
