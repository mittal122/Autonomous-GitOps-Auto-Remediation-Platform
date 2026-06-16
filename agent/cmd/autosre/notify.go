package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/notifier"
)

// runNotify is the entry point for `autosre notify [flags]`.
//
// Defaults to dry-run (prints what it would send). A real send requires both
// configured credentials AND the --send flag. It never executes remediation.
//
// TODO (future prompt — orchestrator): in the live loop, the orchestrator calls
// the notifier automatically after Verify() returns FAILED/INCONCLUSIVE or when
// the policy engine returns REQUIRE_APPROVAL.
// TODO (future prompt — audit): log each send to the audit store.
func runNotify(args []string, cfg config.Config, log *slog.Logger) int {
	fs := flag.NewFlagSet("notify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	notifyType := fs.String("type", "summary", "Notification type: summary | escalate")
	incidentFile := fs.String("incident-file", "", "Path to incident JSON file (omit for built-in fixture)")
	send := fs.Bool("send", false, "Actually send (requires SLACK_BOT_TOKEN/SLACK_CHANNEL_ID or PAGERDUTY_ROUTING_KEY)")
	outputJSON := fs.Bool("json", false, "Print formatted payload as JSON")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	incident, err := loadIncident(*incidentFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading incident: %v\n", err)
		return 1
	}

	switch strings.ToLower(*notifyType) {
	case "summary":
		return runNotifySummary(incident, cfg, log, *send, *outputJSON)
	case "escalate":
		return runNotifyEscalate(incident, cfg, log, *send, *outputJSON)
	default:
		fmt.Fprintf(os.Stderr, "unknown --type %q; expected summary or escalate\n", *notifyType)
		return 2
	}
}

func runNotifySummary(incident contracts.Incident, cfg config.Config, log *slog.Logger, send, asJSON bool) int {
	subject := fmt.Sprintf("[AutoSRE] Incident %s — %s", incident.ID, incident.Severity)
	body := formatSummaryBody(incident)

	if asJSON {
		printNotifyJSON(subject, body)
		return 0
	}

	fmt.Println("=== AutoSRE Notify (summary) ===")
	fmt.Printf("Subject: %s\n", subject)
	fmt.Printf("Body:\n%s\n", body)

	if !send {
		fmt.Println()
		fmt.Println("[DRY-RUN] No message sent. Pass --send to send (requires credentials).")
		return 0
	}

	n := notifier.New(cfg.Notifier, log)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Notifier.SendTimeout+5*time.Second)
	defer cancel()

	if err := n.Notify(ctx, subject, body); err != nil {
		fmt.Fprintf(os.Stderr, "notify error: %v\n", err)
		return 1
	}
	fmt.Println("[SENT] Notification dispatched.")
	return 0
}

func runNotifyEscalate(incident contracts.Incident, cfg config.Config, log *slog.Logger, send, asJSON bool) int {
	reason := fmt.Sprintf("Verification FAILED for incident %s — auto-remediation did not resolve the issue", incident.ID)

	if asJSON {
		payload := map[string]any{
			"type":        "escalation",
			"incident_id": incident.ID,
			"severity":    incident.Severity,
			"resources":   incident.AffectedResources,
			"reason":      reason,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(payload)
		return 0
	}

	fmt.Println("=== AutoSRE Notify (escalate) ===")
	fmt.Printf("Incident:  %s\n", incident.ID)
	fmt.Printf("Severity:  %s\n", incident.Severity)
	fmt.Printf("Resources: %s\n", strings.Join(incident.AffectedResources, ", "))
	fmt.Printf("Reason:    %s\n", reason)

	if !send {
		fmt.Println()
		fmt.Println("[DRY-RUN] No escalation sent. Pass --send to send (requires credentials).")
		return 0
	}

	n := notifier.New(cfg.Notifier, log)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Notifier.SendTimeout+5*time.Second)
	defer cancel()

	if err := n.Escalate(ctx, incident, reason); err != nil {
		fmt.Fprintf(os.Stderr, "escalate error: %v\n", err)
		return 1
	}
	fmt.Println("[SENT] Escalation dispatched (Slack + PagerDuty).")
	return 0
}

func formatSummaryBody(inc contracts.Incident) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Incident ID:  %s\n", inc.ID))
	sb.WriteString(fmt.Sprintf("Severity:     %s\n", inc.Severity))
	sb.WriteString(fmt.Sprintf("Opened:       %s\n", inc.OpenedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Resources:    %s\n", strings.Join(inc.AffectedResources, ", ")))
	sb.WriteString(fmt.Sprintf("Signal count: %d\n", len(inc.Signals)))
	if !inc.ResolvedAt.IsZero() {
		sb.WriteString(fmt.Sprintf("Resolved:     %s (duration %s)\n",
			inc.ResolvedAt.Format(time.RFC3339),
			inc.ResolvedAt.Sub(inc.OpenedAt).Round(time.Second),
		))
	}
	return sb.String()
}

func printNotifyJSON(subject, body string) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]string{"subject": subject, "body": body})
}
