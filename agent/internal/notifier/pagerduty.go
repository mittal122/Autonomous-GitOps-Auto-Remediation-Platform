package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/autosre/agent/internal/contracts"
)

const pdEventsV2URL = "https://events.pagerduty.com/v2/enqueue"

// PagerDutyClient posts trigger events to the PagerDuty Events API v2.
// It is not a full contracts.Notifier; it only handles Escalate.
// The CompositeNotifier combines it with SlackNotifier for the full interface.
type PagerDutyClient struct {
	routingKey string
	client     *http.Client
	maxRetries int
	log        *slog.Logger
}

// NewPagerDutyClient returns a PagerDutyClient. httpClient may be nil.
func NewPagerDutyClient(routingKey string, httpClient *http.Client, maxRetries int, log *slog.Logger) *PagerDutyClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if maxRetries <= 0 {
		maxRetries = 3
	}
	return &PagerDutyClient{
		routingKey: routingKey,
		client:     httpClient,
		maxRetries: maxRetries,
		log:        log,
	}
}

// Trigger sends a PagerDuty "trigger" event for the given incident.
// Degrades to log-only if routingKey is empty; never panics.
func (p *PagerDutyClient) Trigger(ctx context.Context, incident contracts.Incident, reason string) error {
	if p.routingKey == "" {
		p.log.Info("pagerduty trigger (log-only, no routing key)",
			"incident_id", incident.ID, "reason", reason)
		return nil
	}

	payload := pdEventPayload{
		RoutingKey:  p.routingKey,
		EventAction: "trigger",
		DedupKey:    incident.ID,
		Payload: pdPayload{
			Summary:  fmt.Sprintf("[autosre] Incident %s — %s", incident.ID, reason),
			Source:   "autosre-agent",
			Severity: pdSeverity(incident.Severity),
			CustomDetails: map[string]any{
				"incident_id":        incident.ID,
				"affected_resources": strings.Join(incident.AffectedResources, ", "),
				"reason":             reason,
				"opened_at":          incident.OpenedAt.Format(time.RFC3339),
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("pagerduty marshal: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, pdEventsV2URL, bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("pagerduty build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = err
			p.log.Warn("pagerduty send failed, will retry", "attempt", attempt, "error", err)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("pagerduty API status %d", resp.StatusCode)
			p.log.Warn("pagerduty server error, will retry", "status", resp.StatusCode)
			continue
		}
		p.log.Info("pagerduty incident triggered", "incident_id", incident.ID, "status", resp.StatusCode)
		return nil
	}

	p.log.Warn("pagerduty all retries exhausted, degrading to log-only",
		"incident_id", incident.ID, "error", lastErr)
	return nil // degrade, don't propagate
}

// ---------------------------------------------------------------------------
// PagerDuty Events API v2 payload types
// ---------------------------------------------------------------------------

type pdEventPayload struct {
	RoutingKey  string    `json:"routing_key"`
	EventAction string    `json:"event_action"`
	DedupKey    string    `json:"dedup_key"`
	Payload     pdPayload `json:"payload"`
}

type pdPayload struct {
	Summary       string         `json:"summary"`
	Source        string         `json:"source"`
	Severity      string         `json:"severity"`
	CustomDetails map[string]any `json:"custom_details,omitempty"`
}

func pdSeverity(s string) string {
	switch s {
	case "critical":
		return "critical"
	case "warning":
		return "warning"
	default:
		return "error"
	}
}
