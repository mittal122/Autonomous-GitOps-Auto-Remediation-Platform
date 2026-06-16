package orchestrator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/autosre/agent/internal/contracts"
)

// runPipeline executes the 7-stage remediation pipeline for a single incident.
// It is called from a goroutine launched by schedule() and must not block indefinitely.
//
// Stages:
//  1. Diagnose   — call DiagnosisClient; error → skip (fail-closed; no action without diagnosis)
//  2. Propose    — build RemediationProposal from Diagnosis + Incident
//  3. Decide     — evaluate policy; BLOCK → notify+return; REQUIRE_APPROVAL → request+check
//  4. Kill check — abort if kill switch engaged (checked again before Apply in stage 6)
//  5. DryRun     — always call action.DryRun() to produce the human-readable description
//  6. Apply      — only if cfg.ApplyEnabled AND kill switch not engaged
//  7. Verify     — observe recovery window; FAILED/INCONCLUSIVE → Escalate; RECOVERED → Notify
func (o *Orchestrator) runPipeline(ctx context.Context, inc contracts.Incident) {
	log := o.log.With(slog.String("incident_id", inc.ID))
	log.InfoContext(ctx, "orchestrator: pipeline started")

	// -----------------------------------------------------------------------
	// Stage 1: Diagnose
	// -----------------------------------------------------------------------
	diag, err := o.diag.Diagnose(ctx, inc)
	if err != nil {
		log.ErrorContext(ctx, "orchestrator: diagnosis failed; skipping incident", "error", err)
		return
	}
	log.InfoContext(ctx, "orchestrator: diagnosis",
		"failure_mode", diag.FailureMode,
		"proposed_action", diag.ProposedAction,
		"confidence", diag.Confidence,
		"source", diag.Source,
	)

	// -----------------------------------------------------------------------
	// Stage 2: Build proposal
	// -----------------------------------------------------------------------
	proposal := o.buildProposal(inc, diag)

	// -----------------------------------------------------------------------
	// Stage 3: Policy decision
	// -----------------------------------------------------------------------
	decision := o.policy.Evaluate(proposal)
	log.InfoContext(ctx, "orchestrator: policy verdict",
		"verdict", decision.Verdict, "reason", decision.Reason)

	switch decision.Verdict {
	case contracts.VerdictBlock:
		log.InfoContext(ctx, "orchestrator: blocked by policy; no action taken")
		_ = o.notifier.Notify(ctx,
			fmt.Sprintf("BLOCKED — incident %s", inc.ID),
			fmt.Sprintf("Policy engine blocked remediation.\nReason: %s\nRules: %v",
				decision.Reason, decision.MatchedRules),
		)
		return

	case contracts.VerdictRequireApproval:
		log.InfoContext(ctx, "orchestrator: approval required; requesting from notifier")
		ar, err := o.notifier.RequestApproval(ctx, proposal)
		if err != nil {
			// Fail closed: internal notifier error → no action.
			log.ErrorContext(ctx, "orchestrator: approval request error; failing closed", "error", err)
			return
		}
		if ar.Decision != contracts.ApprovalApproved {
			log.InfoContext(ctx, "orchestrator: approval not granted; no action taken",
				"decision", ar.Decision, "approver", ar.Approver, "reason", ar.Reason)
			_ = o.notifier.Notify(ctx,
				fmt.Sprintf("NOT APPROVED — incident %s", inc.ID),
				fmt.Sprintf("Remediation was not approved (decision=%s, approver=%s).\nReason: %s",
					ar.Decision, ar.Approver, ar.Reason),
			)
			return
		}
		log.InfoContext(ctx, "orchestrator: approved", "approver", ar.Approver)

	case contracts.VerdictAuto:
		// All gates passed; proceed without human interaction.

	default:
		// Unknown verdict — fail closed.
		log.ErrorContext(ctx, "orchestrator: unknown policy verdict; failing closed",
			"verdict", decision.Verdict)
		return
	}

	// -----------------------------------------------------------------------
	// Stage 4: Kill switch check (before any action is built or called)
	// -----------------------------------------------------------------------
	if o.kill.Load() {
		log.InfoContext(ctx, "orchestrator: kill switch engaged; no action taken")
		return
	}

	// -----------------------------------------------------------------------
	// Stage 5: Build action and DryRun
	// -----------------------------------------------------------------------
	dryRun := !o.cfg.ApplyEnabled // action is instantiated dry-run when Apply is disabled
	action, err := o.builder.Build(diag, proposal, dryRun)
	if err != nil {
		log.ErrorContext(ctx, "orchestrator: cannot build action", "error", err)
		return
	}

	description, err := action.DryRun(ctx)
	if err != nil {
		log.ErrorContext(ctx, "orchestrator: dry-run failed", "error", err,
			"action", action.Name())
		return
	}
	log.InfoContext(ctx, "orchestrator: dry-run",
		"action", action.Name(), "description", description)

	// -----------------------------------------------------------------------
	// Stage 6: Apply (gated on ApplyEnabled + kill switch)
	// -----------------------------------------------------------------------
	remediationRef := "dry-run/" + inc.ID
	if o.cfg.ApplyEnabled {
		// Re-check kill switch immediately before the write.
		if o.kill.Load() {
			log.InfoContext(ctx, "orchestrator: kill switch engaged before apply; aborting")
			return
		}
		if err := action.Apply(ctx); err != nil {
			log.ErrorContext(ctx, "orchestrator: apply failed; escalating", "error", err,
				"action", action.Name())
			_ = o.notifier.Escalate(ctx, inc,
				fmt.Sprintf("apply failed (action=%s): %v", action.Name(), err))
			return
		}
		remediationRef = "applied/" + inc.ID
		log.InfoContext(ctx, "orchestrator: applied",
			"action", action.Name(), "ref", remediationRef)
	}

	// -----------------------------------------------------------------------
	// Stage 7: Verify + Notify
	// -----------------------------------------------------------------------
	vr := o.verifier.Verify(ctx, inc, remediationRef)
	log.InfoContext(ctx, "orchestrator: verification",
		"outcome", vr.Outcome,
		"escalation_needed", vr.EscalationNeeded,
		"observed_signals", len(vr.ObservedSignals),
		"reason", vr.Reason,
	)

	if vr.EscalationNeeded {
		_ = o.notifier.Escalate(ctx, inc,
			fmt.Sprintf("verification %s (ref=%s): %s", vr.Outcome, remediationRef, vr.Reason))
	} else {
		_ = o.notifier.Notify(ctx,
			fmt.Sprintf("RECOVERED — incident %s (action=%s)", inc.ID, action.Name()),
			fmt.Sprintf("Incident %s was remediated and verified recovered.\n\nAction: %s\nRef: %s\nOutcome: %s\nReason: %s\nSignals observed: %d",
				inc.ID, action.Name(), remediationRef, vr.Outcome, vr.Reason, len(vr.ObservedSignals)),
		)
	}
	log.InfoContext(ctx, "orchestrator: pipeline complete",
		"outcome", vr.Outcome, "ref", remediationRef)
}

// buildProposal converts a Diagnosis + Incident into a RemediationProposal for the policy engine.
// Namespace and Resource come from the first Signal in the Incident (the correlator correlation key).
func (o *Orchestrator) buildProposal(inc contracts.Incident, diag contracts.Diagnosis) contracts.RemediationProposal {
	var ns, resource string
	if len(inc.Signals) > 0 {
		ns = inc.Signals[0].Namespace
		resource = inc.Signals[0].Resource
	}

	return contracts.RemediationProposal{
		IncidentID:  inc.ID,
		Namespace:   ns,
		Resource:    resource,
		FailureMode: diag.FailureMode,
		Confidence:  diag.Confidence,
		Params: contracts.ActionParams{
			ActionType:     diag.ProposedAction,
			TargetReplicas: o.cfg.DefaultScaleReplicas,
			// MemoryBumpFactor intentionally 0 here; ActionBuilder uses its own configured default.
		},
	}
}
