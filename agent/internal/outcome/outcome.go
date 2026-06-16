// Package outcome defines the record type and reporter interface used to post
// completed pipeline outcomes to the learner service.
//
// Safety contract:
//   - Reporter is advisory-only: a Report error is logged and the pipeline continues.
//   - This package never reads from, writes to, or queries the policy engine,
//     gitwriter, or any Kubernetes API.
//   - Learned stats are never fed back into diagnosis or policy. The learner
//     is strictly append-and-read; it does not alter control flow.
package outcome

import (
	"context"
	"time"
)

// Record captures the end-state of one completed remediation pipeline run.
// It is posted to POST /outcome on the learner service.
type Record struct {
	// IncidentID is the correlator-assigned incident identifier.
	IncidentID string `json:"incident_id"`
	// TraceID links this outcome back to the audit trail for the same pipeline run.
	TraceID string `json:"trace_id"`
	// FailureMode is what the diagnoser determined caused the incident (e.g. "OOMKilled").
	FailureMode string `json:"failure_mode"`
	// ProposedAction is what the diagnoser suggested (e.g. "bump-memory-limit").
	ProposedAction string `json:"proposed_action"`
	// Verdict is the policy decision: "AUTO", "REQUIRE_APPROVAL", or "BLOCK".
	Verdict string `json:"verdict"`
	// Applied is true if action.Apply() was called and succeeded.
	Applied bool `json:"applied"`
	// VerificationOutcome is "RECOVERED", "FAILED", "INCONCLUSIVE", or "" if not reached.
	VerificationOutcome string `json:"verification_outcome"`
	// Timestamp is when the pipeline completed.
	Timestamp time.Time `json:"timestamp"`
}

// Reporter posts a completed pipeline outcome to the learner service.
// A nil implementation is a no-op; callers must not call Report on nil.
// The orchestrator wraps calls in a non-fatal helper that checks for nil.
type Reporter interface {
	Report(ctx context.Context, rec Record) error
}
