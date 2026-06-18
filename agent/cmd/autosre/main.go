package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/correlator"
	"github.com/autosre/agent/internal/ingestor"
)

const banner = `
╔══════════════════════════════════════════════════════╗
║   Autonomous GitOps & Auto-Remediation Agent         ║
║   Detection + GitOps Remediation (Prompt 2)          ║
╚══════════════════════════════════════════════════════╝
`

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	log := buildLogger(cfg.LogLevel)

	// Subcommand dispatch.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "remediate":
			os.Exit(runRemediate(os.Args[2:], cfg, log))
		case "policy":
			os.Exit(runPolicy(os.Args[2:], log))
		case "diagnose":
			os.Exit(runDiagnose(os.Args[2:], cfg, log))
		case "verify":
			os.Exit(runVerify(os.Args[2:], cfg, log))
		case "notify":
			os.Exit(runNotify(os.Args[2:], cfg, log))
		case "run":
			os.Exit(runRun(os.Args[2:], cfg, log))
		case "audit":
			os.Exit(runAudit(os.Args[2:], cfg, log))
		}
	}

	fmt.Print(banner)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Build Kubernetes client from in-cluster config or local kubeconfig.
	k8sClient, err := buildK8sClient(cfg)
	if err != nil {
		log.Error("failed to build Kubernetes client", "error", err)
		log.Info("hint: set KUBECONFIG or run inside a cluster with IN_CLUSTER=true")
		os.Exit(1)
	}
	log.Info("Kubernetes client ready")

	// Detection subsystems (main.go doesn't configure Loki — use runRun for the full pipeline).
	ing := ingestor.New(k8sClient, ingestor.LokiConfig{}, log)
	cor := correlator.New(correlator.Config{
		CorrelationWindow: cfg.Correlator.CorrelationWindow,
		ResolveWindow:     cfg.Correlator.ResolveWindow,
		DedupWindow:       cfg.Correlator.DedupWindow,
	}, log)

	// HTTP server: webhook receiver + incident inspector + health check.
	mux := http.NewServeMux()
	mux.Handle("POST /webhook/alertmanager", ing.WebhookHandler())
	mux.Handle("GET /incidents", cor.IncidentsHandler())
	mux.HandleFunc("GET /health", healthHandler)

	srv := &http.Server{
		Addr:         cfg.WebhookAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	var wg sync.WaitGroup

	// Shut down the HTTP server when the root context is cancelled.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			log.Warn("HTTP server shutdown error", "error", err)
		}
	}()

	// Start HTTP server.
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info("HTTP server starting",
			"addr", cfg.WebhookAddr,
			"endpoints", []string{
				"POST /webhook/alertmanager",
				"GET  /incidents",
				"GET  /health",
			},
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP server error", "error", err)
			cancel() // trigger graceful shutdown on unexpected server failure
		}
	}()

	// Start k8s watcher (non-blocking; feeds ing.Signals()).
	ing.Start(ctx)
	log.Info("k8s watcher started")

	// Log incident lifecycle events.
	wg.Add(1)
	go func() {
		defer wg.Done()
		logEvents(cor.Events(), log)
	}()

	// Run correlator — blocks until ctx is cancelled, then closes cor.Events().
	log.Info("correlator running",
		"correlation_window", cfg.Correlator.CorrelationWindow,
		"resolve_window", cfg.Correlator.ResolveWindow,
		"dedup_window", cfg.Correlator.DedupWindow,
	)
	cor.Run(ctx, ing.Signals())

	wg.Wait()
	log.Info("agent stopped cleanly")
}

// logEvents reads IncidentEvents from the channel and logs each one.
// Runs until the channel is closed.
func logEvents(events <-chan correlator.IncidentEvent, log *slog.Logger) {
	for ev := range events {
		switch ev.Kind {
		case correlator.EventOpened:
			log.Info("INCIDENT OPENED",
				"id", ev.Incident.ID,
				"severity", ev.Incident.Severity,
				"affected", ev.Incident.AffectedResources,
				"signals", len(ev.Incident.Signals),
				"opened_at", ev.Incident.OpenedAt.Format(time.RFC3339),
			)
		case correlator.EventUpdated:
			log.Info("INCIDENT UPDATED",
				"id", ev.Incident.ID,
				"signals", len(ev.Incident.Signals),
				"severity", ev.Incident.Severity,
			)
		case correlator.EventClosed:
			duration := ev.Incident.ResolvedAt.Sub(ev.Incident.OpenedAt).Round(time.Second)
			log.Info("INCIDENT CLOSED",
				"id", ev.Incident.ID,
				"duration", duration,
				"signals", len(ev.Incident.Signals),
				// TODO (future prompt): hand incident to policy/remediator
			)
		}
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintln(w, `{"status":"ok"}`)
}

// buildRestConfig resolves the Kubernetes REST config from in-cluster config or kubeconfig.
// Shared by buildK8sClient and any other client (e.g. a dynamic client) that needs the
// same connection details.
func buildRestConfig(cfg config.Config) (*rest.Config, error) {
	if cfg.InCluster {
		restCfg, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("in-cluster config: %w", err)
		}
		return restCfg, nil
	}

	kubeconfig := cfg.Kubeconfig
	if kubeconfig == "" {
		// Fall back to the default kubeconfig location.
		if home := os.Getenv("HOME"); home != "" {
			kubeconfig = home + "/.kube/config"
		}
	}
	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("kubeconfig %q: %w", kubeconfig, err)
	}
	return restCfg, nil
}

// buildK8sClient builds a Kubernetes clientset from in-cluster config or kubeconfig.
func buildK8sClient(cfg config.Config) (kubernetes.Interface, error) {
	restCfg, err := buildRestConfig(cfg)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: %w", err)
	}
	return client, nil
}

func buildLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
