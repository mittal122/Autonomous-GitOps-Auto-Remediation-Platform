package notifier

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/autosre/agent/internal/config"
)

// noopTransport never sends a real request — used to construct clients whose
// internal state we inspect directly, without exercising the network path.
type noopTransport struct{}

func (noopTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
}

func TestCompositeNotifier_Reload_UpdatesUnderlyingClients(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mockHTTP := &http.Client{Transport: noopTransport{}}

	c := &CompositeNotifier{
		slack: NewSlackNotifier(SlackConfig{}, mockHTTP, log),
		pd:    NewPagerDutyClient("", mockHTTP, 0, log),
		log:   log,
	}

	if got := c.slack.snapshotCfg().BotToken; got != "" {
		t.Fatalf("expected empty bot token before reload, got %q", got)
	}
	if got := c.pd.getRoutingKey(); got != "" {
		t.Fatalf("expected empty routing key before reload, got %q", got)
	}

	c.Reload(config.NotifierConfig{
		SlackBotToken:       "xoxb-reloaded",
		SlackSigningSecret:  "reloaded-secret",
		SlackChannelID:      "C00000",
		PagerDutyRoutingKey: "reloaded-routing-key",
	})

	cfg := c.slack.snapshotCfg()
	if cfg.BotToken != "xoxb-reloaded" || cfg.SigningSecret != "reloaded-secret" || cfg.ChannelID != "C00000" {
		t.Errorf("slack credentials not applied: %+v", cfg)
	}
	if got := c.pd.getRoutingKey(); got != "reloaded-routing-key" {
		t.Errorf("pagerduty routing key not applied: %q", got)
	}
}

func TestCompositeNotifier_Reload_PreservesApprovalRegistry(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mockHTTP := &http.Client{Transport: noopTransport{}}

	c := &CompositeNotifier{
		slack: NewSlackNotifier(SlackConfig{}, mockHTTP, log),
		pd:    NewPagerDutyClient("", mockHTTP, 0, log),
		log:   log,
	}
	registryBefore := c.slack.reg

	c.Reload(config.NotifierConfig{
		SlackBotToken:       "xoxb-reloaded",
		SlackSigningSecret:  "reloaded-secret",
		SlackChannelID:      "C00000",
		PagerDutyRoutingKey: "reloaded-routing-key",
	})

	if c.slack.reg != registryBefore {
		t.Error("Reload must not replace the approval registry — in-flight approvals would be lost")
	}
}
