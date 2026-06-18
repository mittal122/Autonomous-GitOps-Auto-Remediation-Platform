package notifier_test

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
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/notifier"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// roundTripFunc adapts a function to http.RoundTripper — lets tests intercept
// all outbound HTTP without starting a real server.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func okTransport(body string) roundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}
}

func errTransport(err error) roundTripFunc {
	return func(r *http.Request) (*http.Response, error) { return nil, err }
}

func serverErrTransport() roundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 500,
			Body:       io.NopCloser(strings.NewReader("server error")),
			Header:     make(http.Header),
		}, nil
	}
}

func testSlackCfg(token, secret, channel string) notifier.SlackConfig {
	return notifier.SlackConfig{
		BotToken:        token,
		SigningSecret:   secret,
		ChannelID:       channel,
		ApprovalTimeout: 200 * time.Millisecond,
		SendTimeout:     5 * time.Second,
		MaxRetries:      1,
	}
}

func testProposal() contracts.RemediationProposal {
	return contracts.RemediationProposal{
		IncidentID:  "inc-test",
		Namespace:   "production",
		Resource:    "payment-service",
		FailureMode: "OOMKilled",
		Params: contracts.ActionParams{
			ActionType:       "bump-memory-limit",
			MemoryBumpFactor: 1.5,
			Container:        "app",
		},
		Confidence: 0.92,
	}
}

func testIncident() contracts.Incident {
	return contracts.Incident{
		ID:                "inc-001",
		AffectedResources: []string{"production/payment-service"},
		Severity:          "critical",
		OpenedAt:          time.Now().Add(-10 * time.Minute),
	}
}

// signSlackRequest computes the Slack HMAC-SHA256 signature for the given body
// and returns the timestamp + signature headers.
func signSlackRequest(secret, body string, age time.Duration) (string, string) {
	ts := strconv.FormatInt(time.Now().Add(-age).Unix(), 10)
	base := "v0:" + ts + ":" + body
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(base))
	sig := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return ts, sig
}

// buildSlackInteractionBody creates a URL-encoded Slack interactions request body.
func buildSlackInteractionBody(blockID, actionValue, userID, userName string) string {
	payload := map[string]any{
		"type": "block_actions",
		"actions": []any{
			map[string]any{
				"action_id": actionValue,
				"block_id":  blockID,
				"value":     actionValue,
			},
		},
		"user": map[string]any{"id": userID, "name": userName},
	}
	b, _ := json.Marshal(payload)
	return "payload=" + url.QueryEscape(string(b))
}

// ---------------------------------------------------------------------------
// SlackNotifier — Notify
// ---------------------------------------------------------------------------

func TestSlackNotifier_Notify_NoCredentials(t *testing.T) {
	cfg := testSlackCfg("", "", "")
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: errTransport(fmt.Errorf("should not call"))}, testLog())

	// No token/channel → log-only, must not error and must not call transport.
	if err := n.Notify(context.Background(), "Test", "body"); err != nil {
		t.Errorf("expected nil error on log-only path, got %v", err)
	}
}

func TestSlackNotifier_Notify_Send(t *testing.T) {
	var captured *http.Request
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured = r
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})

	cfg := testSlackCfg("xoxb-token", "secret", "C12345")
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: transport}, testLog())

	if err := n.Notify(context.Background(), "Incident resolved", "payment-service recovered"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected HTTP request to be sent")
	}
	if !strings.Contains(captured.URL.Path, "chat.postMessage") {
		t.Errorf("expected chat.postMessage endpoint, got %s", captured.URL.Path)
	}
	auth := captured.Header.Get("Authorization")
	if auth != "Bearer xoxb-token" {
		t.Errorf("expected Bearer token header, got %q", auth)
	}
}

func TestSlackNotifier_Notify_TransportError_DegradesToLogOnly(t *testing.T) {
	cfg := testSlackCfg("xoxb-token", "secret", "C12345")
	cfg.MaxRetries = 0
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: errTransport(fmt.Errorf("network down"))}, testLog())

	// Transport error must NOT propagate — degrade to log-only.
	if err := n.Notify(context.Background(), "subj", "body"); err != nil {
		t.Errorf("expected nil (degrade), got %v", err)
	}
}

// ---------------------------------------------------------------------------
// SlackNotifier / PagerDutyClient / CompositeNotifier — Reload
// ---------------------------------------------------------------------------

func TestSlackNotifier_ReloadCredentials_AppliesLive(t *testing.T) {
	var captured *http.Request
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured = r
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})

	cfg := testSlackCfg("", "", "")
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: transport}, testLog())

	// No credentials yet — log-only.
	if err := n.Notify(context.Background(), "subj", "body"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured != nil {
		t.Fatal("expected no HTTP request before ReloadCredentials")
	}

	n.ReloadCredentials("xoxb-new-token", "new-secret", "C99999")

	if err := n.Notify(context.Background(), "subj", "body"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected an HTTP request to be sent after ReloadCredentials")
	}
	if auth := captured.Header.Get("Authorization"); auth != "Bearer xoxb-new-token" {
		t.Errorf("expected new bot token applied, got %q", auth)
	}
}

func TestPagerDutyClient_ReloadRoutingKey_AppliesLive(t *testing.T) {
	var captured *http.Request
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured = r
		return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})

	p := notifier.NewPagerDutyClient("", &http.Client{Transport: transport}, 0, testLog())
	inc := contracts.Incident{ID: "inc-reload-test", Severity: "critical"}

	if err := p.Trigger(context.Background(), inc, "test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured != nil {
		t.Fatal("expected no HTTP request before ReloadRoutingKey")
	}

	p.ReloadRoutingKey("new-routing-key")

	if err := p.Trigger(context.Background(), inc, "test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected an HTTP request to be sent after ReloadRoutingKey")
	}
}

// ---------------------------------------------------------------------------
// SlackNotifier — RequestApproval
// ---------------------------------------------------------------------------

func TestSlackNotifier_RequestApproval_NoCredentials_FailClosed(t *testing.T) {
	cfg := testSlackCfg("", "", "")
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: okTransport("")}, testLog())

	result, err := n.RequestApproval(context.Background(), testProposal())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != contracts.ApprovalDenied {
		t.Errorf("expected DENIED (fail-closed), got %s", result.Decision)
	}
}

func TestSlackNotifier_RequestApproval_Timeout_FailClosed(t *testing.T) {
	cfg := testSlackCfg("xoxb-token", "secret", "C12345")
	cfg.ApprovalTimeout = 50 * time.Millisecond
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: okTransport(`{"ok":true}`)}, testLog())

	result, err := n.RequestApproval(context.Background(), testProposal())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != contracts.ApprovalTimeout {
		t.Errorf("expected TIMEOUT, got %s", result.Decision)
	}
	// TIMEOUT must never equal APPROVED — fail-closed invariant.
	if result.Decision == contracts.ApprovalApproved {
		t.Error("TIMEOUT must not equal APPROVED")
	}
}

func TestSlackNotifier_RequestApproval_ContextCancelled_FailClosed(t *testing.T) {
	cfg := testSlackCfg("xoxb-token", "secret", "C12345")
	cfg.ApprovalTimeout = 5 * time.Second
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: okTransport(`{"ok":true}`)}, testLog())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	result, err := n.RequestApproval(ctx, testProposal())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision == contracts.ApprovalApproved {
		t.Errorf("cancelled context must not produce APPROVED, got %s", result.Decision)
	}
}

func TestSlackNotifier_RequestApproval_PostFails_FailClosed(t *testing.T) {
	cfg := testSlackCfg("xoxb-token", "secret", "C12345")
	cfg.MaxRetries = 0
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: errTransport(fmt.Errorf("network down"))}, testLog())

	result, err := n.RequestApproval(context.Background(), testProposal())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != contracts.ApprovalDenied {
		t.Errorf("expected DENIED when post fails, got %s", result.Decision)
	}
}

func TestSlackNotifier_RequestApproval_Approved(t *testing.T) {
	const secret = "test-signing-secret"
	cfg := testSlackCfg("xoxb-token", secret, "C12345")
	cfg.ApprovalTimeout = 2 * time.Second

	// Track posted messages to extract request ID.
	var postedBlock string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "chat.postMessage") {
			body, _ := io.ReadAll(r.Body)
			// Extract block_id from the JSON to get the request ID.
			var msg struct {
				Blocks []struct {
					BlockID string `json:"block_id"`
				} `json:"blocks"`
			}
			json.Unmarshal(body, &msg)
			for _, b := range msg.Blocks {
				if strings.HasPrefix(b.BlockID, "approval_") {
					postedBlock = b.BlockID
				}
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})

	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: transport}, testLog())
	handler := n.InteractionsHandler()

	resultCh := make(chan contracts.ApprovalResult, 1)
	go func() {
		r, _ := n.RequestApproval(context.Background(), testProposal())
		resultCh <- r
	}()

	// Wait until the approval message is posted and we have the block ID.
	deadline := time.Now().Add(time.Second)
	for postedBlock == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if postedBlock == "" {
		t.Fatal("approval message was not posted")
	}

	// Simulate Slack sending an approve interaction.
	reqBody := buildSlackInteractionBody(postedBlock, "approve", "U999", "alice")
	ts, sig := signSlackRequest(secret, reqBody, 0)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/slack/interactions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", sig)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handler returned %d, want 200", rec.Code)
	}

	result := <-resultCh
	if result.Decision != contracts.ApprovalApproved {
		t.Errorf("expected APPROVED, got %s (reason: %s)", result.Decision, result.Reason)
	}
	if result.Approver != "U999" {
		t.Errorf("expected approver U999, got %s", result.Approver)
	}
}

func TestSlackNotifier_RequestApproval_Denied(t *testing.T) {
	const secret = "test-secret"
	cfg := testSlackCfg("xoxb-token", secret, "C12345")
	cfg.ApprovalTimeout = 2 * time.Second

	var postedBlock string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "chat.postMessage") {
			body, _ := io.ReadAll(r.Body)
			var msg struct {
				Blocks []struct {
					BlockID string `json:"block_id"`
				} `json:"blocks"`
			}
			json.Unmarshal(body, &msg)
			for _, b := range msg.Blocks {
				if strings.HasPrefix(b.BlockID, "approval_") {
					postedBlock = b.BlockID
				}
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})

	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: transport}, testLog())
	handler := n.InteractionsHandler()

	resultCh := make(chan contracts.ApprovalResult, 1)
	go func() {
		r, _ := n.RequestApproval(context.Background(), testProposal())
		resultCh <- r
	}()

	deadline := time.Now().Add(time.Second)
	for postedBlock == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if postedBlock == "" {
		t.Fatal("approval message was not posted")
	}

	reqBody := buildSlackInteractionBody(postedBlock, "deny", "U888", "bob")
	ts, sig := signSlackRequest(secret, reqBody, 0)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/slack/interactions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", sig)
	handler.ServeHTTP(rec, req)

	result := <-resultCh
	if result.Decision != contracts.ApprovalDenied {
		t.Errorf("expected DENIED, got %s", result.Decision)
	}
}

// ---------------------------------------------------------------------------
// Interactions handler — security
// ---------------------------------------------------------------------------

func TestInteractionsHandler_MissingSignature_Rejected(t *testing.T) {
	cfg := testSlackCfg("tok", "secret", "C1")
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: okTransport("")}, testLog())
	handler := n.InteractionsHandler()

	reqBody := buildSlackInteractionBody("approval_x", "approve", "U1", "bob")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/slack/interactions", strings.NewReader(reqBody))
	// No signature headers → must reject.
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestInteractionsHandler_InvalidSignature_Rejected(t *testing.T) {
	cfg := testSlackCfg("tok", "correct-secret", "C1")
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: okTransport("")}, testLog())
	handler := n.InteractionsHandler()

	reqBody := buildSlackInteractionBody("approval_x", "approve", "U1", "bob")
	ts, _ := signSlackRequest("wrong-secret", reqBody, 0) // sign with wrong secret
	_, validSig := signSlackRequest("correct-secret", reqBody, 0)
	// Tamper: use timestamp from wrong-secret but correct sig from different body.
	badSig := "v0=deadbeef0000000000000000000000000000000000000000000000000000dead"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/slack/interactions", strings.NewReader(reqBody))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", badSig)
	_ = validSig
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestInteractionsHandler_ReplayAttack_Rejected(t *testing.T) {
	const secret = "replay-secret"
	cfg := testSlackCfg("tok", secret, "C1")
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: okTransport("")}, testLog())
	handler := n.InteractionsHandler()

	reqBody := buildSlackInteractionBody("approval_x", "approve", "U1", "bob")
	// Use a timestamp 10 minutes old — beyond the 5-minute replay window.
	ts, sig := signSlackRequest(secret, reqBody, 10*time.Minute)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/slack/interactions", strings.NewReader(reqBody))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", sig)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for replay, got %d", rec.Code)
	}
}

func TestInteractionsHandler_UnknownRequestID_NocrashReturn200(t *testing.T) {
	const secret = "my-secret"
	cfg := testSlackCfg("tok", secret, "C1")
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: okTransport("")}, testLog())
	handler := n.InteractionsHandler()

	reqBody := buildSlackInteractionBody("approval_nonexistent99", "approve", "U1", "bob")
	ts, sig := signSlackRequest(secret, reqBody, 0)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/slack/interactions", strings.NewReader(reqBody))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", sig)
	handler.ServeHTTP(rec, req)

	// Must return 200 (not crash) for unknown/expired IDs.
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for unknown ID, got %d", rec.Code)
	}
}

func TestInteractionsHandler_NoSigningSecret_RejectsAll(t *testing.T) {
	cfg := testSlackCfg("tok", "", "C1") // no signing secret
	n := notifier.NewSlackNotifier(cfg, &http.Client{Transport: okTransport("")}, testLog())
	handler := n.InteractionsHandler()

	reqBody := buildSlackInteractionBody("approval_x", "approve", "U1", "bob")
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/slack/interactions", strings.NewReader(reqBody))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", "v0=anything")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when signing secret is empty, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// PagerDuty
// ---------------------------------------------------------------------------

func TestPagerDutyClient_Trigger(t *testing.T) {
	var captured bytes.Buffer
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		io.Copy(&captured, r.Body)
		return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader(`{"status":"success"}`)), Header: make(http.Header)}, nil
	})

	pd := notifier.NewPagerDutyClient("routing-key-123", &http.Client{Transport: transport}, 1, testLog())
	inc := testIncident()

	if err := pd.Trigger(context.Background(), inc, "verification FAILED"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	json.Unmarshal(captured.Bytes(), &payload)
	if payload["routing_key"] != "routing-key-123" {
		t.Errorf("expected routing key in payload, got %v", payload["routing_key"])
	}
	if payload["event_action"] != "trigger" {
		t.Errorf("expected event_action=trigger, got %v", payload["event_action"])
	}
}

func TestPagerDutyClient_NoRoutingKey_LogOnly(t *testing.T) {
	pd := notifier.NewPagerDutyClient("", &http.Client{Transport: errTransport(fmt.Errorf("must not call"))}, 1, testLog())

	// No routing key → log-only, must not error and must not call transport.
	if err := pd.Trigger(context.Background(), testIncident(), "reason"); err != nil {
		t.Errorf("expected nil (log-only), got %v", err)
	}
}

func TestPagerDutyClient_ServerError_DegradesToLogOnly(t *testing.T) {
	pd := notifier.NewPagerDutyClient("key", &http.Client{Transport: serverErrTransport()}, 0, testLog())

	// Server 500 with 0 retries → degrade to log-only, no error propagated.
	if err := pd.Trigger(context.Background(), testIncident(), "reason"); err != nil {
		t.Errorf("expected nil (degrade), got %v", err)
	}
}

// ---------------------------------------------------------------------------
// MockNotifier — interface compliance and recording
// ---------------------------------------------------------------------------

func TestMockNotifier_Interface(t *testing.T) {
	var n contracts.Notifier = &notifier.MockNotifier{}
	_ = n // compile-time check
}

func TestMockNotifier_DefaultDenied(t *testing.T) {
	m := &notifier.MockNotifier{}
	result, err := m.RequestApproval(context.Background(), testProposal())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != contracts.ApprovalDenied {
		t.Errorf("mock default should be DENIED, got %s", result.Decision)
	}
}

func TestMockNotifier_RecordsAllCalls(t *testing.T) {
	m := &notifier.MockNotifier{
		ApprovalResult: contracts.ApprovalResult{
			Decision:  contracts.ApprovalApproved,
			Approver:  "test-user",
			DecidedAt: time.Now(),
		},
	}

	ctx := context.Background()
	inc := testIncident()

	m.Notify(ctx, "subject", "body")
	m.RequestApproval(ctx, testProposal())
	m.Escalate(ctx, inc, "test reason")

	if len(m.Notified) != 1 {
		t.Errorf("expected 1 Notify call, got %d", len(m.Notified))
	}
	if len(m.Approvals) != 1 {
		t.Errorf("expected 1 RequestApproval call, got %d", len(m.Approvals))
	}
	if len(m.Escalated) != 1 {
		t.Errorf("expected 1 Escalate call, got %d", len(m.Escalated))
	}
	if m.Notified[0].Subject != "subject" {
		t.Errorf("Notify subject not recorded correctly")
	}
	if m.Escalated[0].Reason != "test reason" {
		t.Errorf("Escalate reason not recorded correctly")
	}
}

// ---------------------------------------------------------------------------
// Contract types
// ---------------------------------------------------------------------------

func TestContractTypes_ApprovalDecision(t *testing.T) {
	decisions := []contracts.ApprovalDecision{
		contracts.ApprovalApproved,
		contracts.ApprovalDenied,
		contracts.ApprovalTimeout,
	}
	if len(decisions) != 3 {
		t.Fail()
	}

	r := contracts.ApprovalResult{
		RequestID: "req-1",
		Decision:  contracts.ApprovalApproved,
		Approver:  "alice",
		DecidedAt: time.Now(),
		Reason:    "looks good",
	}
	if r.Decision != contracts.ApprovalApproved {
		t.Fail()
	}
	// Verify TIMEOUT is not APPROVED (fail-closed invariant is expressible in code).
	if contracts.ApprovalTimeout == contracts.ApprovalApproved {
		t.Error("TIMEOUT must not equal APPROVED")
	}
}
