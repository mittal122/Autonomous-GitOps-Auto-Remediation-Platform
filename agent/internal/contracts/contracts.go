// Package contracts defines the canonical cross-module interfaces and types for
// the AutoSRE platform. All packages depend on these definitions; none of the
// definitions depend on any other internal package.
//
// Implementations live in the respective internal/* packages and are wired
// together in cmd/autosre/main.go.
package contracts

import (
	"context"
	"time"
)

// ---------------------------------------------------------------------------
// Core data types
// ---------------------------------------------------------------------------

// Signal is a normalized telemetry data point ingested from any source
// (Prometheus alert, Loki log event, Kubernetes watch event, etc.).
type Signal struct {
	// ID uniquely identifies this signal.
	ID string
	// Source identifies the originating system ("k8s-event", "prometheus-alert", "loki-log").
	Source string
	// Namespace and Resource locate the affected Kubernetes resource.
	Namespace string
	// Kind is the Kubernetes resource kind (e.g. "Pod", "Node", "Deployment").
	Kind string
	// Resource is the name of the involved Kubernetes resource.
	Resource string
	// Reason is the normalized failure indicator (e.g. "OOMKilled", "CrashLoopBackOff").
	Reason string
	// Message is a human-readable description from the source system.
	Message string
	// Severity is one of: "critical", "warning", "info".
	Severity string
	// Labels carries arbitrary key/value metadata from the source system.
	Labels map[string]string
	// RawPayload is the original JSON payload for audit purposes.
	RawPayload []byte
	// ReceivedAt is when the agent received this signal.
	ReceivedAt time.Time
}

// Incident represents a correlated set of Signals that together describe a
// single operational problem. The correlator produces Incidents from Signals.
type Incident struct {
	// ID uniquely identifies this incident.
	ID string
	// Signals are the raw telemetry events that triggered this incident.
	Signals []Signal
	// AffectedResources lists Kubernetes resources involved.
	AffectedResources []string
	// Severity is the highest severity among member Signals.
	Severity string
	// OpenedAt is when the first contributing Signal arrived.
	OpenedAt time.Time
	// UpdatedAt is when the most recent Signal was appended.
	UpdatedAt time.Time
	// ResolvedAt is set when the correlator closes this incident (no signals for ResolveWindow).
	// Zero value means the incident is still open.
	ResolvedAt time.Time
}

// Diagnosis is the structured output produced by the LLM diagnoser for an
// Incident. It drives the decision/policy layer. Advisory-only: consuming code
// must not act on it without going through the policy engine first.
type Diagnosis struct {
	// IncidentID links this diagnosis back to the source Incident.
	IncidentID string `json:"incident_id"`
	// RootCause is a human-readable description of the root cause.
	RootCause string `json:"root_cause"`
	// FailureMode categorizes the failure (e.g. "OOMKilled", "CrashLoopBackOff").
	FailureMode string `json:"failure_mode"`
	// ProposedAction is the recommended remediation step (constrained to the action whitelist).
	ProposedAction string `json:"proposed_action"`
	// Confidence is a value in [0, 1] representing the model's certainty.
	Confidence float64 `json:"confidence"`
	// BlastRadius estimates the scope of impact: "pod", "deployment", "namespace", "cluster".
	BlastRadius string `json:"blast_radius"`
	// Source indicates whether this came from the LLM ("gemini") or the rule-based fallback.
	Source string `json:"source"`
	// DiagnosedAt is when the diagnosis was produced.
	DiagnosedAt time.Time `json:"diagnosed_at"`
}

// ---------------------------------------------------------------------------
// Decision / Policy types (Prompt 3)
// ---------------------------------------------------------------------------

// Verdict is the outcome produced by the policy engine for a remediation proposal.
type Verdict string

const (
	// VerdictAuto means all gates passed — the agent may apply automatically.
	VerdictAuto Verdict = "AUTO"
	// VerdictRequireApproval means a human must approve before the action runs.
	VerdictRequireApproval Verdict = "REQUIRE_APPROVAL"
	// VerdictBlock means the action is explicitly forbidden and must not run.
	VerdictBlock Verdict = "BLOCK"
)

// AutonomyLevel controls how much authority the agent has for a given context.
type AutonomyLevel string

const (
	// AutonomyObserve — detect and log only; never propose or act.
	AutonomyObserve AutonomyLevel = "observe"
	// AutonomyPropose — describe what would be done but always require human approval.
	AutonomyPropose AutonomyLevel = "propose"
	// AutonomyAutoWithApproval — act automatically only above the confidence threshold;
	// otherwise require approval.
	AutonomyAutoWithApproval AutonomyLevel = "auto-with-approval"
	// AutonomyFullAuto — act automatically when all policy gates pass.
	AutonomyFullAuto AutonomyLevel = "full-auto"
)

// Decision is the structured output of the policy engine for a single proposal.
type Decision struct {
	// Verdict is the policy engine's final ruling.
	Verdict Verdict
	// Reason is a human-readable explanation of the ruling.
	Reason string
	// MatchedRules lists every policy gate that contributed to (or changed) the verdict,
	// in evaluation order.
	MatchedRules []string
	// DryRunRequired indicates that even an AUTO verdict must first pass a dry-run commit.
	DryRunRequired bool
}

// ActionParams carries the concrete parameters for a proposed remediation action.
// Fields are used by the policy engine to compute blast radius.
type ActionParams struct {
	// ActionType identifies the action: "rollback-deployment", "scale-deployment", "bump-memory-limit".
	ActionType string
	// TargetReplicas is the desired replica count for ScaleDeployment actions.
	TargetReplicas int
	// CurrentReplicas is the current replica count (used to compute delta).
	CurrentReplicas int
	// MemoryBumpFactor is the multiplier for BumpMemoryLimit actions.
	MemoryBumpFactor float64
	// Container is the target container name for rollback and bump-memory actions.
	Container string
	// KnownGoodRef is the target image for rollback actions.
	KnownGoodRef string
}

// RemediationProposal bundles everything the policy engine needs to make a decision.
// It is produced by the orchestrator (future prompt) and evaluated by the policy engine.
type RemediationProposal struct {
	// IncidentID links the proposal to its triggering Incident.
	IncidentID string
	// Namespace and Resource locate the target Kubernetes resource.
	Namespace string
	Resource  string
	// FailureMode is the normalized failure category from the Diagnosis
	// (e.g. "OOMKilled", "CrashLoopBackOff", "BadDeploy").
	FailureMode string
	// Params describes the proposed action and its concrete parameters.
	Params ActionParams
	// Confidence is a value in [0, 1] produced by the diagnoser.
	// TODO (future prompt): supplied by diagnoser/Gemini; currently synthetic in tests.
	Confidence float64
}

// ---------------------------------------------------------------------------
// Core interfaces
// ---------------------------------------------------------------------------

// RemediationAction is a single self-contained corrective action that the
// remediator can execute. Implementations must be idempotent and always
// provide a Rollback path.
//
// TODO (future prompt): concrete implementations go in internal/remediator.
type RemediationAction interface {
	// Name returns a stable, human-readable identifier for this action.
	Name() string
	// DryRun describes what Apply would do without making any changes.
	DryRun(ctx context.Context) (string, error)
	// Apply executes the remediation. It must be idempotent.
	Apply(ctx context.Context) error
	// Rollback undoes the effect of Apply.
	Rollback(ctx context.Context) error
}

// LLMProvider abstracts the underlying LLM so the diagnoser can swap
// backends (Gemini, OpenAI, local model, etc.) without changing callers.
//
// TODO (future prompt): Gemini implementation goes in diagnoser service.
type LLMProvider interface {
	// Diagnose sends the incident context to the LLM and returns a Diagnosis.
	Diagnose(ctx context.Context, incident Incident) (Diagnosis, error)
}

// ---------------------------------------------------------------------------
// Verifier types (Prompt 5)
// ---------------------------------------------------------------------------

// VerificationOutcome is the result of a post-remediation recovery check.
type VerificationOutcome string

const (
	// VerificationRecovered means no matching signals were observed throughout the full window.
	VerificationRecovered VerificationOutcome = "RECOVERED"
	// VerificationFailed means matching signals persisted past the verification window.
	VerificationFailed VerificationOutcome = "FAILED"
	// VerificationInconclusive means the source was unreachable, timed out, or returned ambiguous data.
	VerificationInconclusive VerificationOutcome = "INCONCLUSIVE"
)

// VerificationResult is produced by the Verifier after observing an incident's target.
type VerificationResult struct {
	// IncidentID links this result back to the triggering incident.
	IncidentID string
	// RemediationRef is the git commit SHA or other opaque reference for the applied remediation.
	RemediationRef string
	// Outcome is the verifier's conclusion.
	Outcome VerificationOutcome
	// EscalationNeeded is true when the outcome requires human attention.
	// Always true for FAILED and INCONCLUSIVE; always false for RECOVERED.
	EscalationNeeded bool
	// ObservedSignals is the list of signals seen during the verification window.
	ObservedSignals []Signal
	// WindowStart and WindowEnd bound the observation period (after the grace delay).
	WindowStart time.Time
	WindowEnd   time.Time
	// Reason is a human-readable explanation of the outcome.
	Reason string
}

// ---------------------------------------------------------------------------
// Notifier types (Prompt 6)
// ---------------------------------------------------------------------------

// ApprovalDecision is the outcome of a human approval request.
// TIMEOUT is treated as DENIED downstream — approval fails closed.
type ApprovalDecision string

const (
	// ApprovalApproved means a human explicitly approved the proposed action.
	ApprovalApproved ApprovalDecision = "APPROVED"
	// ApprovalDenied means a human explicitly denied the proposed action.
	ApprovalDenied ApprovalDecision = "DENIED"
	// ApprovalTimeout means no response was received before the deadline.
	// The orchestrator must treat this identically to DENIED (fail-closed).
	ApprovalTimeout ApprovalDecision = "TIMEOUT"
)

// ApprovalResult carries the outcome of a RequestApproval call.
type ApprovalResult struct {
	// RequestID links this result to the original pending request.
	RequestID string
	// Decision is the human's choice, or TIMEOUT if the deadline elapsed.
	Decision ApprovalDecision
	// Approver is the Slack user ID who acted, or "system" for timeout/error.
	Approver string
	// DecidedAt is when the decision was made.
	DecidedAt time.Time
	// Reason is a human-readable explanation (timeout message, denial note, etc.).
	Reason string
}

// ---------------------------------------------------------------------------
// Notifier interface
// ---------------------------------------------------------------------------

// Notifier abstracts all human-facing communication channels
// (Slack, PagerDuty, email, etc.).
//
// Implementations live in internal/notifier. The notifier communicates and
// collects decisions — it never executes remediation or acts on approvals.
// The orchestrator (Prompt 7) acts on the returned ApprovalResult.
//
// Design constraints:
//   - RequestApproval is fail-closed: timeout / missing channel / error → DENIED.
//   - Notify and Escalate degrade gracefully: on transport failure they log and
//     return without panicking or blocking the caller.
type Notifier interface {
	// Notify sends a post-incident summary (what broke, what was done, outcome).
	Notify(ctx context.Context, subject string, body string) error
	// RequestApproval posts a request for a human to approve or deny a proposed
	// remediation. It blocks until the human responds or the approval timeout
	// elapses. On timeout, missing channel, or any error → DENIED (fail-closed).
	// The returned error is non-nil only for unexpected internal failures.
	//
	// TODO (future prompt — orchestrator): caller acts on result.Decision.
	// TODO (future prompt — audit): log ApprovalResult to audit store.
	RequestApproval(ctx context.Context, proposal RemediationProposal) (ApprovalResult, error)
	// Escalate triggers a high-urgency alert (e.g. PagerDuty page) for a
	// FAILED or INCONCLUSIVE verification result.
	Escalate(ctx context.Context, incident Incident, reason string) error
}
