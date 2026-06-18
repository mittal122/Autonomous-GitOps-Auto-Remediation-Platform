package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestGetNotificationsIntegration_NotConfigured(t *testing.T) {
	srv := newTestServerWithLLM(t, nil, false)
	rr := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations/notifications", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var got notificationsIntegrationResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Configured {
		t.Errorf("expected Configured=false, got %+v", got)
	}
}

func TestSaveNotificationsIntegration_PersistsAndRedacts(t *testing.T) {
	srv := newTestServerWithLLM(t, nil, true)
	operator := makeBearer([]string{"operator"})

	body := []byte(`{"slack_bot_token":"xoxb-secret","slack_signing_secret":"sig-secret","slack_channel_id":"C12345","pagerduty_routing_key":"01234567890123456789012345678901"}`)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/notifications", body, operator)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}

	getRR := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations/notifications", nil, operator)
	var got notificationsIntegrationResponse
	if err := json.Unmarshal(getRR.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Configured || got.SlackChannelID != "C12345" || !got.HasSlackBotToken || !got.HasSlackSigningSecret || !got.HasPagerDutyRoutingKey {
		t.Errorf("unexpected response: %+v", got)
	}
	if strings.Contains(getRR.Body.String(), "xoxb-secret") || strings.Contains(getRR.Body.String(), "sig-secret") {
		t.Error("response must not contain plaintext secrets")
	}
}

func TestSaveNotificationsIntegration_OmittedSecretKeepsExisting(t *testing.T) {
	srv := newTestServerWithLLM(t, nil, true)
	operator := makeBearer([]string{"operator"})

	first := []byte(`{"slack_bot_token":"xoxb-original","slack_channel_id":"C12345"}`)
	if rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/notifications", first, operator); rr.Code != http.StatusOK {
		t.Fatalf("first save failed: %d: %s", rr.Code, rr.Body)
	}

	// Second save omits slack_bot_token entirely — must keep the original, not clear it.
	second := []byte(`{"slack_channel_id":"C99999"}`)
	if rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/notifications", second, operator); rr.Code != http.StatusOK {
		t.Fatalf("second save failed: %d: %s", rr.Code, rr.Body)
	}

	getRR := doRequest(t, srv.Handler(""), http.MethodGet, "/api/v1/integrations/notifications", nil, operator)
	var got notificationsIntegrationResponse
	if err := json.Unmarshal(getRR.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.HasSlackBotToken {
		t.Error("expected bot token to survive an update that omitted it")
	}
	if got.SlackChannelID != "C99999" {
		t.Errorf("expected channel updated to C99999, got %q", got.SlackChannelID)
	}
}

func TestSaveNotificationsIntegration_ViewerForbidden(t *testing.T) {
	srv := newTestServerWithLLM(t, nil, true)
	viewer := makeBearer([]string{"viewer"})
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/notifications", []byte(`{}`), viewer)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body)
	}
}

func TestTestNotificationsIntegration_BadChannel(t *testing.T) {
	srv := newTestServerWithLLM(t, nil, false)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/notifications/test", []byte(`{"channel":"email"}`), "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body)
	}
}

func TestTestNotificationsIntegration_SlackMissingToken(t *testing.T) {
	srv := newTestServerWithLLM(t, nil, false)
	rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/notifications/test", []byte(`{"channel":"slack"}`), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var got notificationsTestResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OK {
		t.Error("expected ok=false when slack_bot_token is missing")
	}
}

func TestTestNotificationsIntegration_PagerDutyFormatValidation(t *testing.T) {
	srv := newTestServerWithLLM(t, nil, false)

	cases := []struct {
		key     string
		wantOK  bool
		comment string
	}{
		{"", false, "empty key"},
		{"too-short", false, "wrong length"},
		{"01234567890123456789012345678901", true, "exactly 32 chars"},
	}
	for _, tc := range cases {
		body, _ := json.Marshal(map[string]string{"channel": "pagerduty", "pagerduty_routing_key": tc.key})
		rr := doRequest(t, srv.Handler(""), http.MethodPost, "/api/v1/integrations/notifications/test", body, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("[%s] want 200, got %d: %s", tc.comment, rr.Code, rr.Body)
		}
		var got notificationsTestResult
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("[%s] decode: %v", tc.comment, err)
		}
		if got.OK != tc.wantOK {
			t.Errorf("[%s] expected ok=%v, got %v (%s)", tc.comment, tc.wantOK, got.OK, got.Message)
		}
	}
}
