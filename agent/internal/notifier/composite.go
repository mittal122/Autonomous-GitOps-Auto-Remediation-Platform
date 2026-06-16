package notifier

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
)

// CompositeNotifier implements contracts.Notifier by routing:
//   - Notify        → SlackNotifier
//   - RequestApproval → SlackNotifier (interactive buttons)
//   - Escalate      → SlackNotifier (alert) + PagerDutyClient (page)
type CompositeNotifier struct {
	slack *SlackNotifier
	pd    *PagerDutyClient
	log   *slog.Logger
}

// New returns a CompositeNotifier configured from cfg.
// httpClient may be nil; a default client is used.
func New(cfg config.NotifierConfig, log *slog.Logger) *CompositeNotifier {
	httpClient := &http.Client{Timeout: cfg.SendTimeout}

	slackCfg := SlackConfig{
		BotToken:        cfg.SlackBotToken,
		SigningSecret:   cfg.SlackSigningSecret,
		ChannelID:       cfg.SlackChannelID,
		ApprovalTimeout: cfg.ApprovalTimeout,
		SendTimeout:     cfg.SendTimeout,
		MaxRetries:      cfg.MaxRetries,
	}

	return &CompositeNotifier{
		slack: NewSlackNotifier(slackCfg, httpClient, log),
		pd:    NewPagerDutyClient(cfg.PagerDutyRoutingKey, httpClient, cfg.MaxRetries, log),
		log:   log,
	}
}

func (c *CompositeNotifier) Notify(ctx context.Context, subject, body string) error {
	return c.slack.Notify(ctx, subject, body)
}

func (c *CompositeNotifier) RequestApproval(ctx context.Context, proposal contracts.RemediationProposal) (contracts.ApprovalResult, error) {
	return c.slack.RequestApproval(ctx, proposal)
}

// Escalate sends an alert via Slack AND triggers a PagerDuty incident.
// Both sends are attempted; failure of one does not prevent the other.
func (c *CompositeNotifier) Escalate(ctx context.Context, incident contracts.Incident, reason string) error {
	// Always attempt both; collect errors but don't bail early.
	slackErr := c.slack.Escalate(ctx, incident, reason)
	pdErr := c.pd.Trigger(ctx, incident, reason)

	if slackErr != nil {
		c.log.Warn("composite escalate: slack failed", "error", slackErr, "incident_id", incident.ID)
	}
	if pdErr != nil {
		c.log.Warn("composite escalate: pagerduty failed", "error", pdErr, "incident_id", incident.ID)
	}
	// Both degrade to log-only internally; this should always be nil.
	return nil
}

// InteractionsHandler returns the HTTP handler for POST /slack/interactions.
// Register on the main HTTP mux:
//
//	mux.Handle("POST /slack/interactions", composite.InteractionsHandler())
func (c *CompositeNotifier) InteractionsHandler() http.Handler {
	return c.slack.InteractionsHandler()
}

// ListPendingApprovals returns all currently pending, non-expired approval requests.
// Used by the Web UI to display pending items for operator action.
func (c *CompositeNotifier) ListPendingApprovals() []PendingApproval {
	return c.slack.reg.list()
}

// ResolveApproval resolves a pending approval from the Web UI.
// Routes through the existing fail-closed approval registry — same path as Slack interactive.
// Every resolution must be audited by the caller.
func (c *CompositeNotifier) ResolveApproval(id string, result contracts.ApprovalResult) bool {
	return c.slack.reg.resolve(id, result)
}
