package notifier

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/uid"
)

const (
	slackAPIBase          = "https://slack.com/api"
	slackSignatureVersion = "v0"
	slackMaxClockSkew     = 5 * time.Minute
	approvalBlockPrefix   = "approval_"
)

// SlackConfig holds Slack-specific settings.
type SlackConfig struct {
	BotToken        string
	SigningSecret   string
	ChannelID       string
	ApprovalTimeout time.Duration
	SendTimeout     time.Duration
	MaxRetries      int
}

// SlackNotifier implements contracts.Notifier using the Slack Web API.
// If BotToken or ChannelID is empty, Notify/RequestApproval degrade to log-only.
// If SigningSecret is empty, the interactions endpoint rejects all inbound requests.
type SlackNotifier struct {
	cfg    SlackConfig
	client *http.Client
	reg    *registry
	log    *slog.Logger
}

// NewSlackNotifier returns a SlackNotifier. httpClient may be nil (uses a default
// with SendTimeout). Tests inject a custom transport via httpClient.
func NewSlackNotifier(cfg SlackConfig, httpClient *http.Client, log *slog.Logger) *SlackNotifier {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.SendTimeout}
	}
	if cfg.ApprovalTimeout <= 0 {
		cfg.ApprovalTimeout = 30 * time.Minute
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	return &SlackNotifier{
		cfg:    cfg,
		client: httpClient,
		reg:    newRegistry(),
		log:    log,
	}
}

// Notify posts a plain-text notification to the configured Slack channel.
// Degrades to log-only if credentials are missing; never panics.
func (s *SlackNotifier) Notify(ctx context.Context, subject, body string) error {
	if s.cfg.BotToken == "" || s.cfg.ChannelID == "" {
		s.log.Info("slack notify (log-only, no credentials)", "subject", subject, "body", body)
		return nil
	}
	text := fmt.Sprintf("*%s*\n%s", subject, body)
	payload := map[string]any{
		"channel": s.cfg.ChannelID,
		"text":    text,
	}
	if err := s.postJSON(ctx, "/chat.postMessage", payload); err != nil {
		s.log.Warn("slack notify failed, degrading to log-only", "error", err, "subject", subject)
		return nil // degrade, don't propagate
	}
	return nil
}

// RequestApproval posts an interactive approval request to Slack and blocks
// until the human responds, ctx is cancelled, or the approval timeout elapses.
// Fails closed: any error or timeout → ApprovalDenied.
func (s *SlackNotifier) RequestApproval(ctx context.Context, proposal contracts.RemediationProposal) (contracts.ApprovalResult, error) {
	if s.cfg.BotToken == "" || s.cfg.ChannelID == "" {
		s.log.Warn("slack RequestApproval: no credentials — fail-closed DENIED",
			"incident_id", proposal.IncidentID)
		return denied("", "no Slack credentials configured"), nil
	}

	reqID := uid.New()
	ch := s.reg.register(reqID, proposal, s.cfg.ApprovalTimeout)
	defer s.reg.remove(reqID)

	payload := s.buildApprovalMessage(reqID, proposal)
	if err := s.postJSON(ctx, "/chat.postMessage", payload); err != nil {
		s.log.Warn("slack RequestApproval: failed to post message — fail-closed DENIED",
			"request_id", reqID, "error", err)
		return denied(reqID, fmt.Sprintf("failed to post approval request: %v", err)), nil
	}

	s.log.Info("slack approval requested",
		"request_id", reqID,
		"incident_id", proposal.IncidentID,
		"action", proposal.Params.ActionType,
		"timeout", s.cfg.ApprovalTimeout,
	)

	select {
	case result := <-ch:
		s.log.Info("slack approval received",
			"request_id", reqID, "decision", result.Decision, "approver", result.Approver)
		return result, nil
	case <-time.After(s.cfg.ApprovalTimeout):
		s.log.Warn("slack approval timeout — fail-closed DENIED", "request_id", reqID)
		return contracts.ApprovalResult{
			RequestID: reqID,
			Decision:  contracts.ApprovalTimeout,
			Approver:  "system",
			DecidedAt: time.Now(),
			Reason:    fmt.Sprintf("no response within %s", s.cfg.ApprovalTimeout),
		}, nil
	case <-ctx.Done():
		s.log.Warn("slack approval cancelled — fail-closed DENIED", "request_id", reqID)
		return denied(reqID, "context cancelled"), nil
	}
}

// Escalate posts an escalation alert to Slack.
// Degrades to log-only on transport failure; never panics.
func (s *SlackNotifier) Escalate(ctx context.Context, incident contracts.Incident, reason string) error {
	subject := fmt.Sprintf("[ESCALATION] Incident %s requires attention", incident.ID)
	body := fmt.Sprintf("Severity: %s\nResources: %s\nReason: %s",
		incident.Severity,
		strings.Join(incident.AffectedResources, ", "),
		reason,
	)
	return s.Notify(ctx, subject, body)
}

// InteractionsHandler returns an http.Handler for POST /slack/interactions.
// It verifies the Slack request signature, parses the interaction payload,
// and resolves the corresponding pending approval request.
// Register this on the main HTTP mux:
//
//	mux.Handle("POST /slack/interactions", slackNotifier.InteractionsHandler())
//
// TODO (future prompt — orchestrator): register on main mux in cmd/autosre/main.go.
func (s *SlackNotifier) InteractionsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read body once; needed for both signature verification and payload parsing.
		rawBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB cap
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		// Verify Slack request signature — security boundary.
		timestamp := r.Header.Get("X-Slack-Request-Timestamp")
		signature := r.Header.Get("X-Slack-Signature")
		if !s.verifySignature(timestamp, string(rawBody), signature) {
			s.log.Warn("slack interactions: invalid or missing signature", "remote", r.RemoteAddr)
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		// Parse URL-encoded body; payload field contains JSON.
		vals, err := url.ParseQuery(string(rawBody))
		if err != nil {
			http.Error(w, "malformed body", http.StatusBadRequest)
			return
		}
		payloadJSON := vals.Get("payload")
		if payloadJSON == "" {
			http.Error(w, "missing payload", http.StatusBadRequest)
			return
		}

		var p slackInteractionPayload
		if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
			http.Error(w, "invalid payload JSON", http.StatusBadRequest)
			return
		}

		if len(p.Actions) == 0 {
			http.Error(w, "no actions in payload", http.StatusBadRequest)
			return
		}

		action := p.Actions[0]
		// Request ID is encoded in block_id as "approval_<id>".
		reqID := strings.TrimPrefix(action.BlockID, approvalBlockPrefix)
		if reqID == action.BlockID || reqID == "" {
			// Prefix not found — not our block; ignore cleanly.
			w.WriteHeader(http.StatusOK)
			return
		}

		if s.reg.isExpired(reqID) {
			s.log.Warn("slack interactions: request expired or unknown", "request_id", reqID)
			// Return 200 to Slack (don't retry); just note it's too late.
			fmt.Fprintln(w, `{"text":"This request has already expired or been resolved."}`)
			return
		}

		var decision contracts.ApprovalDecision
		switch strings.ToLower(action.Value) {
		case "approve":
			decision = contracts.ApprovalApproved
		default:
			decision = contracts.ApprovalDenied
		}

		result := contracts.ApprovalResult{
			RequestID: reqID,
			Decision:  decision,
			Approver:  p.User.ID,
			DecidedAt: time.Now(),
			Reason:    fmt.Sprintf("Slack user %s chose %s", p.User.Name, action.Value),
		}

		if !s.reg.resolve(reqID, result) {
			s.log.Warn("slack interactions: could not resolve (already resolved?)", "request_id", reqID)
		} else {
			s.log.Info("slack approval resolved",
				"request_id", reqID, "decision", decision, "approver", p.User.ID)
		}

		// Acknowledge to Slack immediately.
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text":"Decision recorded: %s"}`, decision)
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (s *SlackNotifier) buildApprovalMessage(reqID string, p contracts.RemediationProposal) map[string]any {
	text := fmt.Sprintf(
		"Approval required for *%s* on `%s/%s`\nFailure: `%s` | Confidence: %.0f%%\nAction: `%s`",
		p.Params.ActionType, p.Namespace, p.Resource,
		p.FailureMode, p.Confidence*100, p.Params.ActionType,
	)
	return map[string]any{
		"channel": s.cfg.ChannelID,
		"text":    text,
		"blocks": []any{
			map[string]any{
				"type": "section",
				"text": map[string]any{"type": "mrkdwn", "text": text},
			},
			map[string]any{
				"type":     "actions",
				"block_id": approvalBlockPrefix + reqID,
				"elements": []any{
					map[string]any{
						"type":      "button",
						"action_id": "approve",
						"value":     "approve",
						"style":     "primary",
						"text":      map[string]any{"type": "plain_text", "text": "Approve"},
					},
					map[string]any{
						"type":      "button",
						"action_id": "deny",
						"value":     "deny",
						"style":     "danger",
						"text":      map[string]any{"type": "plain_text", "text": "Deny"},
					},
				},
			},
		},
	}
}

// postJSON marshals payload to JSON and POSTs it to the Slack API with retries.
func (s *SlackNotifier) postJSON(ctx context.Context, path string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= s.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			slackAPIBase+path, bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.cfg.BotToken)

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("slack API %s status %d", path, resp.StatusCode)
			continue
		}
		return nil
	}
	return fmt.Errorf("slack post %s: %w", path, lastErr)
}

// verifySignature validates the Slack request signature.
// Returns false on any validation failure (missing fields, bad HMAC, replay).
func (s *SlackNotifier) verifySignature(timestamp, body, signature string) bool {
	if s.cfg.SigningSecret == "" {
		return false // no secret configured → reject all
	}
	if timestamp == "" || signature == "" {
		return false
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	// Reject replayed requests older than slackMaxClockSkew.
	age := time.Since(time.Unix(ts, 0))
	if age < -slackMaxClockSkew || age > slackMaxClockSkew {
		return false
	}

	base := slackSignatureVersion + ":" + timestamp + ":" + body
	mac := hmac.New(sha256.New, []byte(s.cfg.SigningSecret))
	mac.Write([]byte(base))
	expected := slackSignatureVersion + "=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

// denied returns a fail-closed ApprovalResult with DENIED decision.
func denied(reqID, reason string) contracts.ApprovalResult {
	return contracts.ApprovalResult{
		RequestID: reqID,
		Decision:  contracts.ApprovalDenied,
		Approver:  "system",
		DecidedAt: time.Now(),
		Reason:    reason,
	}
}

// slackInteractionPayload is the JSON body Slack POSTs to /slack/interactions.
type slackInteractionPayload struct {
	Type    string `json:"type"`
	Actions []struct {
		ActionID string `json:"action_id"`
		BlockID  string `json:"block_id"`
		Value    string `json:"value"`
	} `json:"actions"`
	User struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"user"`
}
