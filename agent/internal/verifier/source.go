// Package verifier performs read-only post-remediation recovery checks.
// It observes whether an incident's signals return to normal within a
// configurable window and produces a VerificationResult.
//
// The verifier never writes to the cluster, calls the remediator, or
// invokes the diagnoser/policy engine. Escalation on failure is deferred
// to the notifier (Prompt 6). Orchestrator wiring is Prompt 7.
package verifier

import (
	"time"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/correlator"
)

// RecoverySource is a read-only view of the incident/signal store used by the verifier.
// Keeping this as an interface decouples the verifier from the concrete correlator,
// making it mockable in tests without any live cluster.
type RecoverySource interface {
	// RecentSignalsFor returns all signals recorded for the given target
	// (formatted as "namespace/resource" or "cluster/resource") since the
	// given point in time.
	RecentSignalsFor(target string, since time.Time) []contracts.Signal
	// IsIncidentActive returns true if the named incident is still open
	// (i.e. its ResolvedAt is zero). Returns false for unknown incidents.
	IsIncidentActive(incidentID string) bool
}

// ---------------------------------------------------------------------------
// Correlator-backed implementation
// ---------------------------------------------------------------------------

// CorrelatorSource wraps a *correlator.Correlator to satisfy RecoverySource.
// It is the default production implementation.
type CorrelatorSource struct {
	cor *correlator.Correlator
}

// NewCorrelatorSource returns a RecoverySource backed by the given Correlator.
func NewCorrelatorSource(cor *correlator.Correlator) *CorrelatorSource {
	return &CorrelatorSource{cor: cor}
}

// RecentSignalsFor scans all incidents and returns signals whose resource label
// matches target and whose ReceivedAt is after since.
func (s *CorrelatorSource) RecentSignalsFor(target string, since time.Time) []contracts.Signal {
	incidents := s.cor.ListIncidents()
	var out []contracts.Signal
	for _, inc := range incidents {
		for _, sig := range inc.Signals {
			if resourceLabel(sig) == target && sig.ReceivedAt.After(since) {
				out = append(out, sig)
			}
		}
	}
	return out
}

// IsIncidentActive returns true if the incident with the given ID exists and is still open.
func (s *CorrelatorSource) IsIncidentActive(incidentID string) bool {
	for _, inc := range s.cor.ListIncidents() {
		if inc.ID == incidentID {
			return inc.ResolvedAt.IsZero()
		}
	}
	return false
}

// resourceLabel mirrors the correlator's helper: "namespace/resource" or bare resource.
func resourceLabel(sig contracts.Signal) string {
	if sig.Namespace != "" {
		return sig.Namespace + "/" + sig.Resource
	}
	return sig.Resource
}
