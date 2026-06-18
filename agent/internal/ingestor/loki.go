package ingestor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/uid"
)

// LokiConfig controls the Loki log polling source.
type LokiConfig struct {
	// Addr is the base URL of the Loki instance (e.g. "http://localhost:3100").
	// Empty string disables this source.
	Addr string
	// PollInterval is how often to query Loki for new error logs. Default: 30s.
	PollInterval time.Duration
	// Timeout is the per-request HTTP timeout. Default: 10s.
	Timeout time.Duration
	// Query is the LogQL selector for error-level logs.
	// Default: {namespace=~".+"} — all namespaces; patterns are matched in Go.
	Query string
}

// lokiQueryRangeResp is the JSON body from GET /loki/api/v1/query_range.
type lokiQueryRangeResp struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"` // [[timestampNs, logLine], ...]
		} `json:"result"`
	} `json:"data"`
}

// patternRule maps a log-line regex to a normalized failure mode and proposed action.
type patternRule struct {
	re           *regexp.Regexp
	failureMode  string
	severity     string
	action       string
}

// logPatterns is the ordered list of rules checked against each log line.
// First match wins.
var logPatterns = []patternRule{
	{
		re:          regexp.MustCompile(`(?i)OOMKill`),
		failureMode: "OOMKilled",
		severity:    "critical",
		action:      "bump-memory-limit",
	},
	{
		re:          regexp.MustCompile(`(?i)(CrashLoopBackOff|Back-off restarting failed container)`),
		failureMode: "CrashLoopBackOff",
		severity:    "critical",
		action:      "rollback-deployment",
	},
	{
		re:          regexp.MustCompile(`(?i)(ImagePullBackOff|Failed to pull image|ErrImagePull)`),
		failureMode: "ImagePullBackOff",
		severity:    "warning",
		action:      "rollback-deployment",
	},
	{
		re:          regexp.MustCompile(`(?i)(SERVFAIL|dns.*timeout|i/o timeout.*coredns|coredns.*REFUSED)`),
		failureMode: "DNSSaturation",
		severity:    "warning",
		action:      "scale-deployment",
	},
	{
		re:          regexp.MustCompile(`(?i)(HorizontalPodAutoscaler.*DesiredReplicas|hpa.*oscillat|flapping)`),
		failureMode: "HPAOscillation",
		severity:    "warning",
		action:      "patch-hpa",
	},
	{
		re:          regexp.MustCompile(`(?i)(NodeNotReady|node.*not ready|kubelet.*not.*running)`),
		failureMode: "NodeNotReady",
		severity:    "critical",
		action:      "scale-deployment",
	},
}

// lokiSource polls Loki via HTTP and emits contracts.Signal values.
type lokiSource struct {
	cfg      LokiConfig
	client   *http.Client
	lastSeen time.Time
	log      *slog.Logger

	mu              sync.Mutex
	lastPollAt      time.Time
	lastErr         error
	lastSignalCount int
}

// lokiSourceStatus is a point-in-time snapshot of the poller's health.
type lokiSourceStatus struct {
	lastPollAt      time.Time
	lastErr         error
	lastSignalCount int
}

// status returns the most recent poll's outcome.
func (l *lokiSource) status() lokiSourceStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return lokiSourceStatus{
		lastPollAt:      l.lastPollAt,
		lastErr:         l.lastErr,
		lastSignalCount: l.lastSignalCount,
	}
}

func newLokiSource(cfg LokiConfig, log *slog.Logger) *lokiSource {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.Query == "" {
		cfg.Query = `{namespace=~".+"}`
	}
	return &lokiSource{
		cfg:      cfg,
		client:   &http.Client{Timeout: cfg.Timeout},
		lastSeen: time.Now().Add(-cfg.PollInterval), // start window = one poll interval ago
		log:      log,
	}
}

// Start polls Loki on cfg.PollInterval until ctx is cancelled.
// Signals are written to out; non-blocking: full buffer drops the signal with a warning.
func (l *lokiSource) Start(ctx context.Context, out chan<- contracts.Signal) {
	l.log.Info("loki ingestor: starting", "addr", l.cfg.Addr, "poll_interval", l.cfg.PollInterval)
	ticker := time.NewTicker(l.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			l.log.Info("loki ingestor: stopping")
			return
		case <-ticker.C:
			signals, err := l.poll(ctx)

			l.mu.Lock()
			l.lastPollAt = time.Now()
			l.lastErr = err
			if err == nil {
				l.lastSignalCount = len(signals)
			}
			l.mu.Unlock()

			if err != nil {
				l.log.Warn("loki ingestor: poll error", "error", err)
				continue
			}
			for _, sig := range signals {
				select {
				case out <- sig:
				default:
					l.log.Warn("loki ingestor: signal buffer full, dropping",
						"failure_mode", sig.Reason)
				}
			}
			if len(signals) > 0 {
				l.log.Info("loki ingestor: emitted signals", "count", len(signals))
			}
		}
	}
}

func (l *lokiSource) poll(ctx context.Context) ([]contracts.Signal, error) {
	start := l.lastSeen
	end := time.Now()
	l.lastSeen = end

	rawURL := fmt.Sprintf("%s/loki/api/v1/query_range", strings.TrimRight(l.cfg.Addr, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	q := url.Values{}
	q.Set("query", l.cfg.Query)
	q.Set("start", fmt.Sprintf("%d", start.UnixNano()))
	q.Set("end", fmt.Sprintf("%d", end.UnixNano()))
	q.Set("limit", "200")
	req.URL.RawQuery = q.Encode()

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("loki returned %d: %s", resp.StatusCode, body)
	}

	var result lokiQueryRangeResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return l.extractSignals(result), nil
}

func (l *lokiSource) extractSignals(result lokiQueryRangeResp) []contracts.Signal {
	// Deduplicate by (namespace+failureMode) within a single poll window.
	seen := make(map[string]bool)
	var signals []contracts.Signal

	for _, stream := range result.Data.Result {
		namespace := stream.Stream["namespace"]
		pod := stream.Stream["pod"]
		container := stream.Stream["container"]
		app := firstNonEmpty(stream.Stream["app"], stream.Stream["deployment"], pod)

		for _, entry := range stream.Values {
			if len(entry) < 2 {
				continue
			}
			line := entry[1]

			for _, rule := range logPatterns {
				if !rule.re.MatchString(line) {
					continue
				}

				dedupeKey := namespace + ":" + rule.failureMode
				if seen[dedupeKey] {
					break
				}
				seen[dedupeKey] = true

				labels := map[string]string{
					"source_line": truncate(line, 256),
					"container":   container,
					"app":         app,
				}
				for k, v := range stream.Stream {
					labels[k] = v
				}

				signals = append(signals, contracts.Signal{
					ID:         uid.New(),
					Source:     "loki-log",
					Namespace:  namespace,
					Kind:       inferKindFromStream(stream.Stream),
					Resource:   app,
					Reason:     rule.failureMode,
					Message:    truncate(line, 512),
					Severity:   rule.severity,
					Labels:     labels,
					ReceivedAt: time.Now(),
				})
				break // one signal per log entry (first matching rule wins)
			}
		}
	}
	return signals
}

// LokiTestResult is the outcome of a one-shot Loki connectivity check, used by the
// web API's "Test connection" action before a config is saved.
type LokiTestResult struct {
	OK          bool     `json:"ok"`
	Message     string   `json:"message"`
	SampleLines []string `json:"sample_lines,omitempty"`
}

// TestLokiConnection checks readiness and runs a small sample query against cfg.Addr.
// It does not affect any running poller and is safe to call with an unsaved config.
func TestLokiConnection(ctx context.Context, cfg LokiConfig) LokiTestResult {
	if cfg.Addr == "" {
		return LokiTestResult{Message: "addr is required"}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	addr := strings.TrimRight(cfg.Addr, "/")

	readyReq, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/ready", nil)
	if err != nil {
		return LokiTestResult{Message: fmt.Sprintf("build request: %v", err)}
	}
	readyResp, err := client.Do(readyReq)
	if err != nil {
		return LokiTestResult{Message: fmt.Sprintf("cannot reach %s: %v", cfg.Addr, err)}
	}
	defer readyResp.Body.Close()
	if readyResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(readyResp.Body, 256))
		return LokiTestResult{Message: fmt.Sprintf("loki not ready (HTTP %d): %s", readyResp.StatusCode, body)}
	}

	query := cfg.Query
	if query == "" {
		query = `{namespace=~".+"}`
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/loki/api/v1/query_range", nil)
	if err != nil {
		return LokiTestResult{OK: true, Message: "connected, but could not build sample query"}
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("start", fmt.Sprintf("%d", time.Now().Add(-5*time.Minute).UnixNano()))
	q.Set("end", fmt.Sprintf("%d", time.Now().UnixNano()))
	q.Set("limit", "5")
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return LokiTestResult{OK: true, Message: "connected, but sample query failed: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return LokiTestResult{OK: true, Message: fmt.Sprintf("connected, but query returned HTTP %d: %s", resp.StatusCode, body)}
	}

	var result lokiQueryRangeResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return LokiTestResult{OK: true, Message: "connected, but could not parse sample response"}
	}

	var samples []string
	for _, stream := range result.Data.Result {
		for _, entry := range stream.Values {
			if len(entry) < 2 {
				continue
			}
			samples = append(samples, truncate(entry[1], 256))
			if len(samples) >= 5 {
				break
			}
		}
		if len(samples) >= 5 {
			break
		}
	}

	msg := "Connected"
	if len(samples) == 0 {
		msg = "Connected — no matching log lines in the last 5 minutes (this is normal if the query/namespace has no recent activity)"
	}
	return LokiTestResult{OK: true, Message: msg, SampleLines: samples}
}

func inferKindFromStream(stream map[string]string) string {
	if stream["pod"] != "" {
		return "Pod"
	}
	if stream["deployment"] != "" {
		return "Deployment"
	}
	if stream["node"] != "" {
		return "Node"
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
