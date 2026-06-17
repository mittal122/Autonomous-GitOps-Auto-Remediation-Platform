package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// WebhookSink ships audit events to an HTTP endpoint (Splunk HEC, Elastic, Loki push, etc.).
// Events are sent one-at-a-time over a buffered channel; a background goroutine drains the
// channel and posts to the configured URL. Failures are logged and swallowed — the pipeline
// must never block on a failing SIEM exporter.
//
// Start() must be called once to launch the background drainer.
// Stop() must be called on shutdown to drain remaining events.
type WebhookSink struct {
	url     string
	timeout time.Duration
	client  *http.Client
	log     *slog.Logger

	ch   chan AuditEvent
	done chan struct{}
	once sync.Once
}

// WebhookSinkOptions configures the WebhookSink.
type WebhookSinkOptions struct {
	// URL is the HTTPS/HTTP endpoint that receives POST requests.
	// Each event is sent as a JSON body with Content-Type: application/json.
	URL string
	// Timeout is the per-request deadline. Default: 5s.
	Timeout time.Duration
	// BufferSize is the channel buffer depth. Default: 256.
	BufferSize int
}

// NewWebhookSink creates a WebhookSink. Call Start() to begin forwarding events.
func NewWebhookSink(opts WebhookSinkOptions, log *slog.Logger) *WebhookSink {
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	bufSize := opts.BufferSize
	if bufSize <= 0 {
		bufSize = 256
	}
	return &WebhookSink{
		url:     opts.URL,
		timeout: opts.Timeout,
		client:  &http.Client{Timeout: opts.Timeout},
		log:     log,
		ch:      make(chan AuditEvent, bufSize),
		done:    make(chan struct{}),
	}
}

// Start launches the background drainer goroutine.
// It must be called exactly once; subsequent calls are no-ops.
func (s *WebhookSink) Start(ctx context.Context) {
	s.once.Do(func() {
		go s.drain(ctx)
	})
}

// Stop flushes remaining buffered events and waits for the drainer to exit.
// Blocks until the channel is empty or ctx is cancelled.
func (s *WebhookSink) Stop() {
	close(s.ch)
	<-s.done
}

// Record enqueues ev for asynchronous forwarding.
// If the buffer is full the event is dropped and an error is returned
// (the caller logs this but must not block).
func (s *WebhookSink) Record(_ context.Context, ev AuditEvent) error {
	select {
	case s.ch <- ev:
		return nil
	default:
		return fmt.Errorf("audit/webhook: buffer full, event dropped (incident=%s stage=%s)",
			ev.IncidentID, ev.Stage)
	}
}

// Query is not supported on WebhookSink (events are forwarded one-way).
func (s *WebhookSink) Query(_ context.Context, _ QueryFilter) ([]AuditEvent, error) {
	return nil, nil
}

// drain reads from the channel and POSTs each event.
func (s *WebhookSink) drain(ctx context.Context) {
	defer close(s.done)
	for ev := range s.ch {
		s.post(ctx, ev)
	}
}

func (s *WebhookSink) post(ctx context.Context, ev AuditEvent) {
	body, err := json.Marshal(ev)
	if err != nil {
		s.log.WarnContext(ctx, "audit/webhook: marshal failed", "error", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		s.log.WarnContext(ctx, "audit/webhook: build request failed", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		s.log.WarnContext(ctx, "audit/webhook: POST failed (event dropped)", "error", err, "url", s.url)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.log.WarnContext(ctx, "audit/webhook: non-2xx response",
			"status", resp.StatusCode, "url", s.url)
	}
}

// compile-time interface assertion
var _ AuditSink = (*WebhookSink)(nil)
