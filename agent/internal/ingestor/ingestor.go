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
	"log/slog"
	"net/http"

	"k8s.io/client-go/kubernetes"

	"github.com/autosre/agent/internal/contracts"
)

const signalBufSize = 512

// Ingestor aggregates all signal sources and streams normalized contracts.Signal
// values on a single read-only channel.
type Ingestor struct {
	signals chan contracts.Signal
	watcher *k8sWatcher
	loki    *lokiSource // nil when LokiConfig.Addr is empty
	log     *slog.Logger
}

// New creates an Ingestor wired to the given Kubernetes client.
// Pass a non-empty LokiConfig.Addr to enable Loki log polling.
func New(client kubernetes.Interface, lokiCfg LokiConfig, log *slog.Logger) *Ingestor {
	ch := make(chan contracts.Signal, signalBufSize)
	ing := &Ingestor{
		signals: ch,
		watcher: newK8sWatcher(client, log),
		log:     log,
	}
	if lokiCfg.Addr != "" {
		ing.loki = newLokiSource(lokiCfg, log)
	}
	return ing
}

// Start launches the Kubernetes watcher and (if configured) Loki poller as
// background goroutines. Returns immediately; the caller must consume
// Signals() to prevent the buffer from filling.
func (i *Ingestor) Start(ctx context.Context) {
	go i.watcher.Start(ctx, i.signals)
	if i.loki != nil {
		go i.loki.Start(ctx, i.signals)
	}
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
