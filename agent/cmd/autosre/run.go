package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/autosre/agent/internal/api"
	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/correlator"
	"github.com/autosre/agent/internal/diagnosis"
	"github.com/autosre/agent/internal/gitwriter"
	"github.com/autosre/agent/internal/ingestor"
	"github.com/autosre/agent/internal/notifier"
	"github.com/autosre/agent/internal/orchestrator"
	"github.com/autosre/agent/internal/outcome"
	"github.com/autosre/agent/internal/policy"
	"github.com/autosre/agent/internal/verifier"
)

// runRun starts the full reconcile loop: detect → diagnose → decide → remediate → verify → notify.
//
// DRY-RUN-ONLY by default. No GitOps commits are made unless --apply is passed
// (or ORCHESTRATOR_APPLY_ENABLED=true is set in the environment).
func runRun(args []string, cfg config.Config, log *slog.Logger) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "enable real GitOps commits (overrides ORCHESTRATOR_APPLY_ENABLED)")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		return 1
	}
	if *apply {
		cfg.Orchestrator.ApplyEnabled = true
	}

	if !cfg.Orchestrator.ApplyEnabled {
		log.Info("orchestrator: DRY-RUN mode — no GitOps commits will be made (pass --apply to enable)")
	} else {
		log.Warn("orchestrator: APPLY mode — real GitOps commits ENABLED")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// -----------------------------------------------------------------------
	// Components
	// -----------------------------------------------------------------------

	gw := gitwriter.New(gitwriter.Config{
		RepoPath: cfg.Remediator.RepoPath,
		BotName:  cfg.Remediator.BotName,
		BotEmail: cfg.Remediator.BotEmail,
		Branch:   cfg.Remediator.Branch,
	}, log)

	polCfg, err := policy.LoadPolicyFile(cfg.Orchestrator.PolicyFile)
	if err != nil {
		log.Warn("policy: using fail-closed defaults", "reason", err)
	}
	pol := policy.New(polCfg, log)

	diagClient := diagnosis.NewClient(diagnosis.Config{
		Addr:    cfg.Diagnoser.Addr,
		Timeout: cfg.Diagnoser.Timeout,
	})

	notif := notifier.New(cfg.Notifier, log)

	cor := correlator.New(correlator.Config{
		CorrelationWindow: cfg.Correlator.CorrelationWindow,
		ResolveWindow:     cfg.Correlator.ResolveWindow,
		DedupWindow:       cfg.Correlator.DedupWindow,
	}, log)

	ver := verifier.New(cfg.Verifier, verifier.NewCorrelatorSource(cor), log)

	builder := orchestrator.NewDefaultBuilder(
		gw,
		cfg.Orchestrator.DefaultContainer,
		cfg.Orchestrator.DefaultScaleReplicas,
		cfg.Remediator.MemoryBumpFactor,
		log,
	)

	// Audit sink: append-only JSONL file (no-op if disabled).
	var auditSink audit.AuditSink = audit.NoOp{}
	if cfg.Audit.Enabled {
		fs, fsErr := audit.NewFileSink(cfg.Audit.FilePath)
		if fsErr != nil {
			log.Warn("audit: cannot open file sink; audit disabled",
				"error", fsErr, "path", cfg.Audit.FilePath)
		} else {
			auditSink = fs
			log.Info("audit: file sink opened", "path", cfg.Audit.FilePath)
		}
	}

	// Outcome client: posts to learner POST /outcome (nil → disabled).
	var outcomeClient outcome.Reporter
	if cfg.Learner.Addr != "" {
		outcomeClient = outcome.NewClient(cfg.Learner.Addr, cfg.Learner.Timeout, log)
		log.Info("outcome: learner client configured", "addr", cfg.Learner.Addr)
	}

	orch := orchestrator.New(cfg.Orchestrator, diagClient, pol, notif, ver, builder,
		auditSink, outcomeClient, log)

	// -----------------------------------------------------------------------
	// Kubernetes client + ingestor (best-effort; webhook still active without it)
	// -----------------------------------------------------------------------
	k8sClient, k8sErr := buildK8sClient(cfg)
	if k8sErr != nil {
		log.Warn("k8s client unavailable; k8s watcher disabled (webhook ingestion still active)",
			"error", k8sErr)
	}

	var ing *ingestor.Ingestor
	if k8sClient != nil {
		lokiCfg := ingestor.LokiConfig{
			Addr:         cfg.Loki.Addr,
			PollInterval: cfg.Loki.PollInterval,
			Timeout:      cfg.Loki.Timeout,
			Query:        cfg.Loki.Query,
		}
		ing = ingestor.New(k8sClient, lokiCfg, log)
		ing.Start(ctx)
	}

	// -----------------------------------------------------------------------
	// HTTP server
	// -----------------------------------------------------------------------
	mux := http.NewServeMux()

	if ing != nil {
		mux.Handle("POST /webhook/alertmanager", ing.WebhookHandler())
	} else {
		mux.HandleFunc("POST /webhook/alertmanager", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "k8s client unavailable; ingestor not running", http.StatusServiceUnavailable)
		})
	}

	mux.Handle("GET /incidents", cor.IncidentsHandler())
	mux.HandleFunc("GET /health", healthHandler)
	mux.Handle("POST /slack/interactions", notif.InteractionsHandler())

	// Web API + UI (catch-all; specific routes above take precedence).
	apiSrv := api.NewServer(ctx, cfg.API, cor, orch, auditSink, notif, pol, cfg.Learner.Addr, log)
	mux.Handle("/", apiSrv.Handler(cfg.API.WebUIDir))

	srv := &http.Server{
		Addr:         cfg.WebhookAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		if shutErr := srv.Shutdown(shutCtx); shutErr != nil {
			log.Warn("HTTP server shutdown error", "error", shutErr)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info("HTTP server starting",
			"addr", cfg.WebhookAddr,
			"oidc_enabled", cfg.API.OIDCEnabled,
			"apply_enabled", cfg.Orchestrator.ApplyEnabled,
			"endpoints", []string{
				"POST /webhook/alertmanager",
				"GET  /incidents",
				"GET  /health",
				"POST /slack/interactions",
				"GET  /api/v1/incidents",
				"POST /api/v1/approvals/{id}/approve",
				"POST /api/v1/approvals/{id}/reject",
				"POST /api/v1/control/kill-switch (admin)",
			},
		)
		if serveErr := srv.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			log.Error("HTTP server error", "error", serveErr)
			cancel()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		orch.Run(ctx, cor.Events())
	}()

	// Run correlator — blocks until ctx is cancelled, then closes cor.Events().
	log.Info("correlator starting",
		"correlation_window", cfg.Correlator.CorrelationWindow,
		"resolve_window", cfg.Correlator.ResolveWindow,
	)
	var src <-chan contracts.Signal
	if ing != nil {
		src = ing.Signals()
	} else {
		src = make(chan contracts.Signal) // never-sending; correlator runs but gets no k8s signals
	}
	cor.Run(ctx, src)

	wg.Wait()
	log.Info("autosre run: stopped cleanly")
	return 0
}
