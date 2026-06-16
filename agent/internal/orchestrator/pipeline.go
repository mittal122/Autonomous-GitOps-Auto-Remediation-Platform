package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/outcome"
	"github.com/autosre/agent/internal/uid"
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
//
// Every stage emits an AuditEvent (non-fatal). A completed-pipeline OutcomeRecord is
// posted to the learner service at the end (non-fatal).
func (o *Orchestrator) runPipeline(ctx context.Context, inc contracts.Incident) {
	traceID := uid.New()
	log := o.log.With(
		slog.String("incident_id", inc.ID),
		slog.String("trace_id", traceID),
	)
	log.InfoContext(ctx, "orchestrator: pipeline started")

	// outcomeRec is populated once we have enough information to post to the learner.
	// nil → not enough data collected (diagnosis failed before policy stage).
	var outcomeRec *outcome.Record
	defer func() {
		if outcomeRec != nil {
			outcomeRec.Timestamp = time.Now()
			o.reportOutcome(ctx, *outcomeRec)
		}
	}()

	// -----------------------------------------------------------------------
	// Stage 1: Diagnose
	// -----------------------------------------------------------------------
	o.record(ctx, traceID, inc.ID, audit.StageDetected, "started", map[string]string{
		"severity": inc.Severity,
	})

	diag, err := o.diag.Diagnose(ctx, inc)
	if err != nil {
		log.ErrorContext(ctx, "orchestrator: diagnosis failed; skipping incident", "error", err)
		o.record(ctx, traceID, inc.ID, audit.StageDiagnosed, "error", map[string]string{
			"error": err.Error(),
		})
		return
	}
	log.InfoContext(ctx, "orchestrator: diagnosis",
		"failure_mode", diag.FailureMode,
		"proposed_action", diag.ProposedAction,
		"confidence", diag.Confidence,
		"source", diag.Source,
	)
	o.record(ctx, traceID, inc.ID, audit.StageDiagnosed, "ok", map[string]string{
		"failure_mode":    diag.FailureMode,
		"proposed_action": diag.ProposedAction,
		"confidence":      fmt.Sprintf("%.4f", diag.Confidence),
		"source":          diag.Source,
	})

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
	o.record(ctx, traceID, inc.ID, audit.StageDecided, string(decision.Verdict), map[string]string{
		"verdict": string(decision.Verdict),
		"reason":  decision.Reason,
	})

	// Populate the outcome record now that we have all decision-stage fields.
	outcomeRec = &outcome.Record{
		IncidentID:     inc.ID,
		TraceID:        traceID,
		FailureMode:    diag.FailureMode,
		ProposedAction: diag.ProposedAction,
		Verdict:        string(decision.Verdict),
	}

	switch decision.Verdict {
	case contracts.VerdictBlock:
		log.InfoContext(ctx, "orchestrator: blocked by policy; no action taken")
		_ = o.notifier.Notify(ctx,
			fmt.Sprintf("BLOCKED — incident %s", inc.ID),
			fmt.Sprintf("Policy engine blocked remediation.\nReason: %s\nRules: %v",
				decision.Reason, decision.MatchedRules),
		)
		o.record(ctx, traceID, inc.ID, audit.StageNotified, "ok", map[string]string{
			"subject": fmt.Sprintf("BLOCKED — incident %s", inc.ID),
		})
		return

	case contracts.VerdictRequireApproval:
		log.InfoContext(ctx, "orchestrator: approval required; requesting from notifier")
		o.record(ctx, traceID, inc.ID, audit.StageApprovalRequested, "ok", map[string]string{
			"namespace":   proposal.Namespace,
			"action_type": proposal.Params.ActionType,
		})

		ar, err := o.notifier.RequestApproval(ctx, proposal)
		if err != nil {
			// Fail closed: internal notifier error → no action.
			log.ErrorContext(ctx, "orchestrator: approval request error; failing closed", "error", err)
			o.record(ctx, traceID, inc.ID, audit.StageApprovalResolved, "error", map[string]string{
				"error": err.Error(),
			})
			return
		}
		o.record(ctx, traceID, inc.ID, audit.StageApprovalResolved, string(ar.Decision), map[string]string{
			"decision": string(ar.Decision),
			"approver": ar.Approver,
			"reason":   ar.Reason,
		})
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
		o.record(ctx, traceID, inc.ID, audit.StageDryRun, "error", map[string]string{
			"error": err.Error(),
		})
		return
	}

	description, err := action.DryRun(ctx)
	if err != nil {
		log.ErrorContext(ctx, "orchestrator: dry-run failed", "error", err,
			"action", action.Name())
		o.record(ctx, traceID, inc.ID, audit.StageDryRun, "error", map[string]string{
			"action": action.Name(),
			"error":  err.Error(),
		})
		return
	}
	log.InfoContext(ctx, "orchestrator: dry-run",
		"action", action.Name(), "description", description)
	o.record(ctx, traceID, inc.ID, audit.StageDryRun, "ok", map[string]string{
		"action":      action.Name(),
		"description": description,
	})

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
			o.record(ctx, traceID, inc.ID, audit.StageApplied, "error", map[string]string{
				"action": action.Name(),
				"error":  err.Error(),
			})
			_ = o.notifier.Escalate(ctx, inc,
				fmt.Sprintf("apply failed (action=%s): %v", action.Name(), err))
			o.record(ctx, traceID, inc.ID, audit.StageEscalated, "ok", map[string]string{
				"reason": fmt.Sprintf("apply failed: %v", err),
			})
			return
		}
		outcomeRec.Applied = true
		remediationRef = "applied/" + inc.ID
		log.InfoContext(ctx, "orchestrator: applied",
			"action", action.Name(), "ref", remediationRef)
		o.record(ctx, traceID, inc.ID, audit.StageApplied, "ok", map[string]string{
			"action": action.Name(),
			"ref":    remediationRef,
		})
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
	outcomeRec.VerificationOutcome = string(vr.Outcome)

	o.record(ctx, traceID, inc.ID, audit.StageVerified, string(vr.Outcome), map[string]string{
		"outcome":          string(vr.Outcome),
		"reason":           vr.Reason,
		"observed_signals": fmt.Sprintf("%d", len(vr.ObservedSignals)),
		"ref":              remediationRef,
	})

	if vr.EscalationNeeded {
		_ = o.notifier.Escalate(ctx, inc,
			fmt.Sprintf("verification %s (ref=%s): %s", vr.Outcome, remediationRef, vr.Reason))
		o.record(ctx, traceID, inc.ID, audit.StageEscalated, "ok", map[string]string{
			"reason": fmt.Sprintf("verification %s: %s", vr.Outcome, vr.Reason),
		})
	} else {
		_ = o.notifier.Notify(ctx,
			fmt.Sprintf("RECOVERED — incident %s (action=%s)", inc.ID, action.Name()),
			fmt.Sprintf("Incident %s was remediated and verified recovered.\n\nAction: %s\nRef: %s\nOutcome: %s\nReason: %s\nSignals observed: %d",
				inc.ID, action.Name(), remediationRef, vr.Outcome, vr.Reason, len(vr.ObservedSignals)),
		)
		o.record(ctx, traceID, inc.ID, audit.StageNotified, "ok", map[string]string{
			"subject": fmt.Sprintf("RECOVERED — incident %s (action=%s)", inc.ID, action.Name()),
		})
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
