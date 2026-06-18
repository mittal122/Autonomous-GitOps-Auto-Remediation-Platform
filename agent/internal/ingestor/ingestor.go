// Package ingestor normalizes raw cluster telemetry into contracts.Signal values
// and streams them for downstream correlation. It is strictly read-only: it
// never writes to or modifies any Kubernetes resource.
//
// Active signal sources:
//   - Kubernetes event/pod/node watcher (via client-go informers)
//   - Alertmanager webhook receiver (POST /webhook/alertmanager)
//   - Loki log polling (GET /loki/api/v1/query_range) — enabled when LokiConfig.Addr is set
package ingestor

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/autosre/agent/internal/contracts"
)

const signalBufSize = 512

// Ingestor aggregates all signal sources and streams normalized contracts.Signal
// values on a single read-only channel.
type Ingestor struct {
	signals chan contracts.Signal
	watcher *k8sWatcher
	log     *slog.Logger

	mu         sync.Mutex
	parentCtx  context.Context
	loki       *lokiSource        // nil when Loki is disabled
	lokiCfg    LokiConfig         // last config passed to ReloadLoki (or New)
	lokiCancel context.CancelFunc // cancels the running Loki goroutine, if any
}

// New creates an Ingestor wired to the given Kubernetes client.
// Pass a non-empty LokiConfig.Addr to enable Loki log polling once Start is called.
func New(client kubernetes.Interface, lokiCfg LokiConfig, log *slog.Logger) *Ingestor {
	return &Ingestor{
		signals: make(chan contracts.Signal, signalBufSize),
		watcher: newK8sWatcher(client, log),
		log:     log,
		lokiCfg: lokiCfg,
	}
}

// Start launches the Kubernetes watcher and (if configured) Loki poller as
// background goroutines. Returns immediately; the caller must consume
// Signals() to prevent the buffer from filling.
func (i *Ingestor) Start(ctx context.Context) {
	i.mu.Lock()
	i.parentCtx = ctx
	cfg := i.lokiCfg
	i.mu.Unlock()

	go i.watcher.Start(ctx, i.signals)
	if cfg.Addr != "" {
		if err := i.ReloadLoki(cfg); err != nil {
			i.log.Warn("ingestor: failed to start loki source", "error", err)
		}
	}
}

// ReloadLoki stops the current Loki poller (if any) and starts a new one with cfg,
// without affecting the Kubernetes watcher or requiring a process restart. Passing
// a LokiConfig with an empty Addr disables Loki polling entirely.
//
// Start must be called at least once before ReloadLoki.
func (i *Ingestor) ReloadLoki(cfg LokiConfig) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.lokiCancel != nil {
		i.lokiCancel()
		i.lokiCancel = nil
		i.loki = nil
	}
	i.lokiCfg = cfg

	if cfg.Addr == "" {
		i.log.Info("ingestor: loki source disabled")
		return nil
	}
	if i.parentCtx == nil {
		return fmt.Errorf("ingestor: Start must be called before ReloadLoki")
	}

	childCtx, cancel := context.WithCancel(i.parentCtx)
	src := newLokiSource(cfg, i.log)
	i.loki = src
	i.lokiCancel = cancel
	go src.Start(childCtx, i.signals)
	return nil
}

// LokiStatus reports the current Loki integration health for display in the API/UI.
func (i *Ingestor) LokiStatus() LokiStatus {
	i.mu.Lock()
	src := i.loki
	cfg := i.lokiCfg
	i.mu.Unlock()

	if src == nil {
		return LokiStatus{Enabled: false}
	}
	st := src.status()
	out := LokiStatus{
		Enabled:         true,
		Addr:            cfg.Addr,
		LastPollAt:      st.lastPollAt,
		LastSignalCount: st.lastSignalCount,
	}
	if st.lastErr != nil {
		out.LastError = st.lastErr.Error()
	}
	return out
}

// LokiStatus summarizes the Loki poller's health for API/UI consumption.
type LokiStatus struct {
	Enabled         bool      `json:"enabled"`
	Addr            string    `json:"addr,omitempty"`
	LastPollAt      time.Time `json:"last_poll_at,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	LastSignalCount int       `json:"last_signal_count"`
}

// Signals returns the read-only channel of normalized signal events.
func (i *Ingestor) Signals() <-chan contracts.Signal {
	return i.signals
}

// WebhookHandler returns an http.Handler that accepts Alertmanager webhook
// payloads (POST /webhook/alertmanager) and converts them to Signals.
func (i *Ingestor) WebhookHandler() http.Handler {
	return alertmanagerHandler(i.signals, i.log)
}
