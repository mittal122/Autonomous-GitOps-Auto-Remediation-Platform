package ingestor

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"
)

func emptyLokiResponse(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"streams","result":[]}}`)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestIngestor_LokiStatus_DisabledByDefault(t *testing.T) {
	ing := New(fake.NewSimpleClientset(), LokiConfig{}, discardLogger())
	if st := ing.LokiStatus(); st.Enabled {
		t.Errorf("expected Loki disabled when no Addr configured, got %+v", st)
	}
}

func TestIngestor_ReloadLoki_EnablesAndPolls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(emptyLokiResponse))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ing := New(fake.NewSimpleClientset(), LokiConfig{}, discardLogger())
	ing.Start(ctx)

	if st := ing.LokiStatus(); st.Enabled {
		t.Fatalf("expected disabled before ReloadLoki, got %+v", st)
	}

	if err := ing.ReloadLoki(LokiConfig{Addr: srv.URL, PollInterval: 20 * time.Millisecond, Timeout: time.Second}); err != nil {
		t.Fatalf("ReloadLoki failed: %v", err)
	}

	waitForCondition(t, time.Second, func() bool {
		return ing.LokiStatus().Enabled && !ing.LokiStatus().LastPollAt.IsZero()
	})

	st := ing.LokiStatus()
	if !st.Enabled || st.Addr != srv.URL {
		t.Errorf("expected enabled with addr %q, got %+v", srv.URL, st)
	}
	if st.LastError != "" {
		t.Errorf("expected no error, got %q", st.LastError)
	}
}

func TestIngestor_ReloadLoki_SwapsAddrWithoutRestart(t *testing.T) {
	srv1 := httptest.NewServer(http.HandlerFunc(emptyLokiResponse))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(emptyLokiResponse))
	defer srv2.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ing := New(fake.NewSimpleClientset(), LokiConfig{Addr: srv1.URL, PollInterval: 20 * time.Millisecond, Timeout: time.Second}, discardLogger())
	ing.Start(ctx)

	waitForCondition(t, time.Second, func() bool { return ing.LokiStatus().Addr == srv1.URL })

	if err := ing.ReloadLoki(LokiConfig{Addr: srv2.URL, PollInterval: 20 * time.Millisecond, Timeout: time.Second}); err != nil {
		t.Fatalf("ReloadLoki failed: %v", err)
	}

	waitForCondition(t, time.Second, func() bool { return ing.LokiStatus().Addr == srv2.URL })

	if st := ing.LokiStatus(); st.Addr != srv2.URL {
		t.Errorf("expected addr swapped to %q, got %q", srv2.URL, st.Addr)
	}
}

func TestIngestor_ReloadLoki_EmptyAddrDisables(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(emptyLokiResponse))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ing := New(fake.NewSimpleClientset(), LokiConfig{Addr: srv.URL, PollInterval: 20 * time.Millisecond, Timeout: time.Second}, discardLogger())
	ing.Start(ctx)

	waitForCondition(t, time.Second, func() bool { return ing.LokiStatus().Enabled })

	if err := ing.ReloadLoki(LokiConfig{}); err != nil {
		t.Fatalf("ReloadLoki(disable) failed: %v", err)
	}

	if st := ing.LokiStatus(); st.Enabled {
		t.Errorf("expected disabled after empty-Addr reload, got %+v", st)
	}
}

func TestIngestor_ReloadLoki_BeforeStart_Errors(t *testing.T) {
	ing := New(fake.NewSimpleClientset(), LokiConfig{}, discardLogger())
	if err := ing.ReloadLoki(LokiConfig{Addr: "http://example.com"}); err == nil {
		t.Error("expected error when calling ReloadLoki before Start")
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
