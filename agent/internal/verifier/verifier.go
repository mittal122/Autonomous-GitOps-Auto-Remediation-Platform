package verifier

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
)

// Verifier checks whether a remediated incident has recovered.
// It is read-only: it never calls the remediator, gitwriter, or k8s.
type Verifier struct {
	source config.VerifierConfig
	src    RecoverySource
	log    *slog.Logger
}

// New returns a Verifier that observes recovery via the given RecoverySource.
// Panics if cfg.PollInterval or cfg.Window is non-positive (caller must validate).
func New(cfg config.VerifierConfig, src RecoverySource, log *slog.Logger) *Verifier {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 15 * time.Second
	}
	if cfg.Window <= 0 {
		cfg.Window = 5 * time.Minute
	}
	if cfg.GraceDelay < 0 {
		cfg.GraceDelay = 0
	}
	if cfg.FailureThreshold < 0 {
		cfg.FailureThreshold = 0
	}
	return &Verifier{source: cfg, src: src, log: log}
}

// Verify observes the target resource for the given incident and returns a
// VerificationResult. It blocks for up to GraceDelay + Window.
//
// Core principle: FAIL TOWARD ESCALATION.
// Only RECOVERED when no matching signals appear throughout the full window.
// Any error, timeout, or ambiguity → INCONCLUSIVE with EscalationNeeded=true.
//
// TODO (future prompt — orchestrator): invoke Verify after Apply() returns.
func (v *Verifier) Verify(ctx context.Context, incident contracts.Incident, remediationRef string) contracts.VerificationResult {
	target := incidentTarget(incident)

	v.log.Info("verifier starting",
		"incident_id", incident.ID,
		"target", target,
		"remediation_ref", remediationRef,
		"grace_delay", v.source.GraceDelay,
		"window", v.source.Window,
		"poll_interval", v.source.PollInterval,
	)

	// --- Phase 1: grace delay ---
	// Wait for ArgoCD to sync the git commit before judging recovery.
	if v.source.GraceDelay > 0 {
		select {
		case <-ctx.Done():
			return v.inconclusive(incident.ID, remediationRef, time.Time{}, time.Time{},
				nil, "context cancelled during grace delay")
		case <-time.After(v.source.GraceDelay):
		}
	}

	windowStart := time.Now()
	windowEnd := windowStart.Add(v.source.Window)

	v.log.Info("verifier observing",
		"incident_id", incident.ID,
		"window_start", windowStart.Format(time.RFC3339),
		"window_end", windowEnd.Format(time.RFC3339),
	)

	// --- Phase 2: polling loop ---
	ticker := time.NewTicker(v.source.PollInterval)
	defer ticker.Stop()

	var allObserved []contracts.Signal

	for {
		select {
		case <-ctx.Done():
			return v.inconclusive(incident.ID, remediationRef, windowStart, time.Now(),
				allObserved, "context cancelled during observation window")

		case now := <-ticker.C:
			signals := v.src.RecentSignalsFor(target, windowStart)
			// De-dup: only count new ones not already in allObserved.
			allObserved = mergeSignals(allObserved, signals)

			if len(allObserved) > v.source.FailureThreshold {
				v.log.Warn("verifier: signals persist — FAILED",
					"incident_id", incident.ID,
					"observed", len(allObserved),
					"threshold", v.source.FailureThreshold,
				)
				// TODO (future prompt — notifier): hand FAILED result to notifier for escalation.
				return contracts.VerificationResult{
					IncidentID:       incident.ID,
					RemediationRef:   remediationRef,
					Outcome:          contracts.VerificationFailed,
					EscalationNeeded: true,
					ObservedSignals:  allObserved,
					WindowStart:      windowStart,
					WindowEnd:        now,
					Reason: fmt.Sprintf(
						"observed %d signal(s) for %q past failure threshold (%d) — escalation needed",
						len(allObserved), target, v.source.FailureThreshold,
					),
				}
			}

			if now.After(windowEnd) || now.Equal(windowEnd) {
				// Full window elapsed with signals ≤ threshold: declare recovered.
				v.log.Info("verifier: full window clean — RECOVERED",
					"incident_id", incident.ID,
					"window", v.source.Window,
				)
				return contracts.VerificationResult{
					IncidentID:       incident.ID,
					RemediationRef:   remediationRef,
					Outcome:          contracts.VerificationRecovered,
					EscalationNeeded: false,
					ObservedSignals:  allObserved,
					WindowStart:      windowStart,
					WindowEnd:        now,
					Reason: fmt.Sprintf(
						"no signals for %q over %s window — incident resolved",
						target, v.source.Window,
					),
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (v *Verifier) inconclusive(
	incidentID, remediationRef string,
	windowStart, windowEnd time.Time,
	observed []contracts.Signal,
	reason string,
) contracts.VerificationResult {
	v.log.Warn("verifier: INCONCLUSIVE", "incident_id", incidentID, "reason", reason)
	// TODO (future prompt — notifier): hand INCONCLUSIVE result to notifier for escalation.
	return contracts.VerificationResult{
		IncidentID:       incidentID,
		RemediationRef:   remediationRef,
		Outcome:          contracts.VerificationInconclusive,
		EscalationNeeded: true,
		ObservedSignals:  observed,
		WindowStart:      windowStart,
		WindowEnd:        windowEnd,
		Reason:           reason,
	}
}

// incidentTarget returns the "namespace/resource" or bare resource string
// that the verifier will ask the RecoverySource about.
func incidentTarget(incident contracts.Incident) string {
	if len(incident.AffectedResources) > 0 {
		return incident.AffectedResources[0]
	}
	return incident.ID
}

// mergeSignals appends signals from next that are not already in acc (by signal ID).
func mergeSignals(acc, next []contracts.Signal) []contracts.Signal {
	seen := make(map[string]struct{}, len(acc))
	for _, s := range acc {
		seen[s.ID] = struct{}{}
	}
	for _, s := range next {
		if _, ok := seen[s.ID]; !ok {
			acc = append(acc, s)
		}
	}
	return acc
}
