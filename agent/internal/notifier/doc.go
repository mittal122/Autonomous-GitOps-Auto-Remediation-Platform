// Package notifier implements the contracts.Notifier interface with concrete
// Slack and PagerDuty backends. It communicates and collects decisions; it
// never executes remediation or acts on approvals.
//
// Key design decisions:
//   - Approval is a security boundary: RequestApproval fails closed on timeout,
//     missing config, or any transport error.
//   - The inbound Slack interactions endpoint verifies Slack request signatures
//     (HMAC-SHA256 with signing secret + timestamp; replays >5m old are rejected).
//   - All outbound sends use per-call timeouts and capped retries; transport
//     failures degrade to log-only, never panic or block the caller.
//   - No external SDK dependencies — only net/http + std library.
//
// TODO (future prompt — orchestrator): wire SlackNotifier.InteractionsHandler()
// onto the main HTTP mux so inbound approvals can resolve pending requests.
// TODO (future prompt — audit): log each Notify/RequestApproval/Escalate call
// to the audit store.
package notifier

import "github.com/autosre/agent/internal/contracts"

// Compile-time interface assertions.
var (
	_ contracts.Notifier = (*SlackNotifier)(nil)
	_ contracts.Notifier = (*CompositeNotifier)(nil)
	_ contracts.Notifier = (*MockNotifier)(nil)
)
