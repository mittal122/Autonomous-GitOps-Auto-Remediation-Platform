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
