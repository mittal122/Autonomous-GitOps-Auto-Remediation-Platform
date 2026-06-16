// Package audit provides an append-only event log for every stage of every
// pipeline run. It is the system's source of truth for "what happened and why."
//
// Safety contract:
//   - AuditSink has Record (write) and Query (read) only. No Update, no Delete.
//   - Recording is non-fatal: a sink error is logged and the pipeline continues.
//   - Audit never calls remediator, gitwriter, policy engine, or any k8s API.
//   - A grep of this package for Apply/Commit/Scale/Exec must return clean.
package audit

import (
	"context"
	"time"
)

// Stage identifies which step of the remediation pipeline produced an event.
type Stage string

const (
	StageDetected          Stage = "Detected"
	StageDiagnosed         Stage = "Diagnosed"
	StageDecided           Stage = "Decided"
	StageApprovalRequested Stage = "ApprovalRequested"
	StageApprovalResolved  Stage = "ApprovalResolved"
	StageDryRun            Stage = "DryRun"
	StageApplied           Stage = "Applied"
	StageVerified          Stage = "Verified"
	StageNotified          Stage = "Notified"
	StageEscalated         Stage = "Escalated"
)

// AuditEvent is a single immutable record of one pipeline stage.
// All events for the same pipeline run share the same TraceID.
type AuditEvent struct {
	// Timestamp is when the event was recorded.
	Timestamp time.Time `json:"timestamp"`
	// TraceID links all events of one pipeline run together.
	TraceID string `json:"trace_id"`
	// IncidentID links this event to the triggering incident.
	IncidentID string `json:"incident_id"`
	// Stage identifies the pipeline step.
	Stage Stage `json:"stage"`
	// Outcome is a short string describing the result: "ok", "error", "blocked",
	// "approved", "denied", "timeout", "recovered", "failed", "inconclusive", "started".
	Outcome string `json:"outcome"`
	// Details holds stage-specific key/value metadata.
	Details map[string]string `json:"details,omitempty"`
}

// QueryFilter selects a subset of audit events.
// Zero values mean "no restriction" for that field.
type QueryFilter struct {
	// IncidentID restricts to events for this incident.
	IncidentID string
	// TraceID restricts to events with this trace ID.
	TraceID string
	// Stage restricts to events at this stage (empty = all stages).
	Stage Stage
	// Since excludes events before this time (zero = no lower bound).
	Since time.Time
	// Until excludes events after this time (zero = no upper bound).
	Until time.Time
	// Limit caps the number of returned events (0 = no limit).
	Limit int
}

// matches reports whether ev satisfies f.
func (f QueryFilter) matches(ev AuditEvent) bool {
	if f.IncidentID != "" && ev.IncidentID != f.IncidentID {
		return false
	}
	if f.TraceID != "" && ev.TraceID != f.TraceID {
		return false
	}
	if f.Stage != "" && ev.Stage != f.Stage {
		return false
	}
	if !f.Since.IsZero() && ev.Timestamp.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && ev.Timestamp.After(f.Until) {
		return false
	}
	return true
}

// AuditSink is the write+read interface for the audit log.
// Implementations must be append-only: Record writes a new event; Query reads
// existing events. There is intentionally no Update or Delete method.
type AuditSink interface {
	// Record appends a new event. Errors are non-fatal to the caller; the
	// pipeline must continue even if recording fails.
	Record(ctx context.Context, event AuditEvent) error
	// Query returns events matching the filter, ordered by Timestamp ascending.
	Query(ctx context.Context, filter QueryFilter) ([]AuditEvent, error)
}

// NoOp silently discards all events. Used when audit is disabled.
type NoOp struct{}

func (NoOp) Record(_ context.Context, _ AuditEvent) error             { return nil }
func (NoOp) Query(_ context.Context, _ QueryFilter) ([]AuditEvent, error) { return nil, nil }

// compile-time interface assertion
var _ AuditSink = NoOp{}
