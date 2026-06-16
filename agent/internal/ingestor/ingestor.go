// Package ingestor normalizes raw cluster telemetry into contracts.Signal values
// and streams them for downstream correlation. It is strictly read-only: it
// never writes to or modifies any Kubernetes resource.
//
// Two signal sources are active in this prompt:
//   - Kubernetes event/pod/node watcher (via client-go informers)
//   - Alertmanager webhook receiver (POST /webhook/alertmanager)
//
// TODO (future prompt): add Loki log-tail source.
package ingestor

import (
	"context"
	"log/slog"
	"net/http"

	"k8s.io/client-go/kubernetes"

	"github.com/autosre/agent/internal/contracts"
)

const signalBufSize = 256

// Ingestor aggregates all signal sources and streams normalized contracts.Signal
// values on a single read-only channel.
type Ingestor struct {
	signals chan contracts.Signal
	watcher *k8sWatcher
	log     *slog.Logger
}

// New creates an Ingestor wired to the given Kubernetes client.
func New(client kubernetes.Interface, log *slog.Logger) *Ingestor {
	ch := make(chan contracts.Signal, signalBufSize)
	return &Ingestor{
		signals: ch,
		watcher: newK8sWatcher(client, log),
		log:     log,
	}
}

// Start launches the Kubernetes watcher in a background goroutine.
// It returns immediately; the caller must consume Signals() to prevent
// the buffer from filling.
// Start is idempotent: calling it more than once is safe but wasteful.
func (i *Ingestor) Start(ctx context.Context) {
	go i.watcher.Start(ctx, i.signals)
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
