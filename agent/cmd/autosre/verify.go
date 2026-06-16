package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/verifier"
)

// runVerify is the entry point for `autosre verify [flags]`.
// It loads an incident, constructs a mock RecoverySource from a signals file
// (or uses an empty one), runs the Verifier, and prints the VerificationResult.
//
// The verify subcommand EXECUTES NOTHING: no remediation, no cluster writes,
// no escalation calls. It observes only.
//
// TODO (future prompt — orchestrator): in the live loop, the orchestrator invokes
// the verifier automatically after a remediation action's Apply() returns.
// TODO (future prompt — notifier): on FAILED/INCONCLUSIVE, pass the result to
// the notifier for Slack/PagerDuty escalation.
func runVerify(args []string, cfg config.Config, log *slog.Logger) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	incidentFile := fs.String("incident-file", "", "Path to incident JSON file (omit to use built-in fixture)")
	remediationRef := fs.String("remediation-ref", "", "Git commit SHA or remediation reference (informational)")
	outputJSON := fs.Bool("json", false, "Output result as JSON")
	graceDelay := fs.Duration("grace-delay", cfg.Verifier.GraceDelay, "Grace delay before observing (overrides VERIFIER_GRACE_DELAY)")
	window := fs.Duration("window", cfg.Verifier.Window, "Observation window (overrides VERIFIER_WINDOW)")
	pollInterval := fs.Duration("poll-interval", cfg.Verifier.PollInterval, "Poll interval (overrides VERIFIER_POLL_INTERVAL)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	incident, err := loadIncident(*incidentFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading incident: %v\n", err)
		return 1
	}

	vcfg := config.VerifierConfig{
		GraceDelay:       *graceDelay,
		Window:           *window,
		PollInterval:     *pollInterval,
		FailureThreshold: cfg.Verifier.FailureThreshold,
	}

	// In the CLI the recovery source is a static snapshot of the incident's own
	// signals — there is no live correlator in the one-shot CLI path.
	// The verifier sees the incident's signals as pre-existing and judges recovery
	// based on whether new ones appear (which they won't in CLI mode → RECOVERED).
	src := &cliRecoverySource{incident: incident}

	v := verifier.New(vcfg, src, log)

	ctx, cancel := context.WithTimeout(context.Background(),
		*graceDelay+*window+5*time.Second)
	defer cancel()

	log.Info("verify: starting",
		"incident_id", incident.ID,
		"grace_delay", *graceDelay,
		"window", *window,
	)

	result := v.Verify(ctx, incident, *remediationRef)

	if *outputJSON {
		return printVerificationJSON(result)
	}
	return printVerificationText(result)
}

// cliRecoverySource is a static RecoverySource for the one-shot CLI.
// It returns no signals during the observation window (signals were received
// before the window opened), so the CLI always reports RECOVERED for a clean
// incident file — useful for smoke-testing the verifier plumbing.
type cliRecoverySource struct {
	incident contracts.Incident
}

func (s *cliRecoverySource) RecentSignalsFor(_ string, since time.Time) []contracts.Signal {
	// Return only signals that arrived after the window start (i.e. new signals).
	// For the CLI fixture the signals were all received at load time (before the
	// observation window starts), so none will appear → simulates clean recovery.
	var out []contracts.Signal
	for _, sig := range s.incident.Signals {
		if sig.ReceivedAt.After(since) {
			out = append(out, sig)
		}
	}
	return out
}

func (s *cliRecoverySource) IsIncidentActive(_ string) bool {
	return s.incident.ResolvedAt.IsZero()
}

func printVerificationText(r contracts.VerificationResult) int {
	fmt.Println("=== AutoSRE Verification Result ===")
	fmt.Printf("Incident:          %s\n", r.IncidentID)
	fmt.Printf("Remediation ref:   %s\n", r.RemediationRef)
	fmt.Printf("Outcome:           %s\n", r.Outcome)
	fmt.Printf("Escalation needed: %v\n", r.EscalationNeeded)
	fmt.Printf("Observed signals:  %d\n", len(r.ObservedSignals))
	if !r.WindowStart.IsZero() {
		fmt.Printf("Window:            %s → %s\n",
			r.WindowStart.Format(time.RFC3339),
			r.WindowEnd.Format(time.RFC3339),
		)
	}
	fmt.Printf("Reason:            %s\n", r.Reason)
	fmt.Println()
	switch r.Outcome {
	case contracts.VerificationRecovered:
		fmt.Println("RECOVERED — incident marked resolved (internal state only; no cluster write).")
	case contracts.VerificationFailed:
		fmt.Println("FAILED — escalation needed. (Notifier is Prompt 6.)")
	case contracts.VerificationInconclusive:
		fmt.Println("INCONCLUSIVE — escalation needed. (Notifier is Prompt 6.)")
	}
	return 0
}

func printVerificationJSON(r contracts.VerificationResult) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		fmt.Fprintf(os.Stderr, "json encode error: %v\n", err)
		return 1
	}
	return 0
}
