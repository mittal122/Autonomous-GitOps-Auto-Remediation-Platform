// Package e2e contains end-to-end tests for the AutoSRE remediation pipeline.
// Tests use in-process components and local bare git repos — no network, no k8s.
package e2e_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/config"
	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/correlator"
	"github.com/autosre/agent/internal/diagnosis"
	"github.com/autosre/agent/internal/gitwriter"
	"github.com/autosre/agent/internal/orchestrator"
	"github.com/autosre/agent/internal/policy"
	"github.com/go-git/go-git/v5"
	gitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ---------------------------------------------------------------------------
// Shared test fixtures
// ---------------------------------------------------------------------------

const deploymentManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: payment-service
  namespace: production
spec:
  replicas: 3
  template:
    spec:
      containers:
        - name: app
          image: myapp/payment-service:v1.4.2
          resources:
            limits:
              memory: "256Mi"
`

// initBareAndClone creates a bare repo (acts as remote) and a clone seeded with
// the given manifest content. It returns both directory paths.
func initBareAndClone(t *testing.T) (bareDir, workDir string) {
	t.Helper()

	bareDir = t.TempDir()
	workDir = t.TempDir()

	// Create bare repo.
	_, err := git.PlainInit(bareDir, true)
	if err != nil {
		t.Fatalf("init bare repo: %v", err)
	}

	// Create working repo and seed manifest.
	workRepo, err := git.PlainInit(workDir, false)
	if err != nil {
		t.Fatalf("init work repo: %v", err)
	}

	manifestPath := filepath.Join(workDir, "apps", "production", "payment-service.yaml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte(deploymentManifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	wt, err := workRepo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if _, err := wt.Add("apps/production/payment-service.yaml"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	sig := &object.Signature{Name: "test", Email: "test@e2e.dev", When: time.Now()}
	if _, err := wt.Commit("chore: seed manifest", &git.CommitOptions{Author: sig}); err != nil {
		t.Fatalf("initial commit: %v", err)
	}

	// Wire remote and push initial commit to bare repo.
	if _, err := workRepo.CreateRemote(&gitcfg.RemoteConfig{
		Name: "origin",
		URLs: []string{bareDir},
	}); err != nil {
		t.Fatalf("create remote: %v", err)
	}
	if err := workRepo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []gitcfg.RefSpec{"refs/heads/master:refs/heads/master"},
	}); err != nil && err != git.NoErrAlreadyUpToDate {
		t.Fatalf("initial push: %v", err)
	}

	return bareDir, workDir
}

// countCommitsInBare returns the number of commits reachable from HEAD in the bare repo.
func countCommitsInBare(bareDir string) int {
	repo, err := git.PlainOpen(bareDir)
	if err != nil {
		return 0
	}
	ref, err := repo.Head()
	if err != nil {
		return 0
	}
	iter, err := repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return 0
	}
	defer iter.Close()
	var n int
	_ = iter.ForEach(func(_ *object.Commit) error { n++; return nil })
	return n
}

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

type mockNotifier struct{}

func (m *mockNotifier) Notify(_ context.Context, _, _ string) error { return nil }
func (m *mockNotifier) RequestApproval(_ context.Context, proposal contracts.RemediationProposal) (contracts.ApprovalResult, error) {
	return contracts.ApprovalResult{
		RequestID: "mock-req",
		Decision:  contracts.ApprovalApproved,
		Approver:  "e2e-test",
		DecidedAt: time.Now(),
	}, nil
}
func (m *mockNotifier) Escalate(_ context.Context, _ contracts.Incident, _ string) error { return nil }

type mockVerifier struct{}

func (m *mockVerifier) Verify(_ context.Context, inc contracts.Incident, _ string) contracts.VerificationResult {
	return contracts.VerificationResult{
		IncidentID:       inc.ID,
		Outcome:          contracts.VerificationRecovered,
		EscalationNeeded: false,
		Reason:           "mock: immediately recovered",
	}
}

// ---------------------------------------------------------------------------
// Test 1: gitwriter push to a local bare repo
// ---------------------------------------------------------------------------

func TestGitwriterPushToLocalBare(t *testing.T) {
	bareDir, workDir := initBareAndClone(t)

	gw := gitwriter.New(gitwriter.Config{
		RepoPath:  workDir,
		BotName:   "autosre-e2e",
		BotEmail:  "e2e@autosre.dev",
		Branch:    "master",
		RemoteURL: bareDir,
	}, slog.Default())

	ctx := context.Background()
	res, err := gw.EditField(ctx,
		"production", "payment-service", "Deployment",
		"spec.replicas", "5", false)
	if err != nil {
		t.Fatalf("EditField: %v", err)
	}
	if res.CommitSHA == "" {
		t.Error("expected non-empty CommitSHA")
	}
	if res.OldValue != "3" || res.NewValue != "5" {
		t.Errorf("values: old=%q new=%q", res.OldValue, res.NewValue)
	}

	// The commit must have been pushed to the bare repo.
	n := countCommitsInBare(bareDir)
	if n < 2 {
		t.Errorf("expected at least 2 commits in bare repo after push, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// Test 2: EnsureRepo clones from a pre-populated bare repo
// ---------------------------------------------------------------------------

func TestEnsureRepo_ClonesFromBare(t *testing.T) {
	bareDir, _ := initBareAndClone(t)

	cloneDir := filepath.Join(t.TempDir(), "clone")

	gw := gitwriter.New(gitwriter.Config{
		RepoPath:  cloneDir,
		BotName:   "autosre-e2e",
		BotEmail:  "e2e@autosre.dev",
		Branch:    "master",
		RemoteURL: bareDir,
	}, slog.Default())

	ctx := context.Background()
	if err := gw.EnsureRepo(ctx); err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}

	// Clone should contain the manifest.
	manifestPath := filepath.Join(cloneDir, "apps", "production", "payment-service.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("manifest not found in clone: %v", err)
	}
	if len(data) == 0 {
		t.Error("manifest is empty")
	}
}

// ---------------------------------------------------------------------------
// Test 3: diagnosis client retries on transient server errors
// ---------------------------------------------------------------------------

func TestDiagnosisClient_RetriesOnServerError(t *testing.T) {
	var attempts atomic.Int32

	sampleResp := map[string]any{
		"incident_id":     "test-inc",
		"failure_mode":    "CrashLoopBackOff",
		"proposed_action": "scale-deployment",
		"confidence":      0.95,
		"blast_radius":    "deployment",
		"root_cause":      "container restarting repeatedly",
		"source":          "test",
		"diagnosed_at":    time.Now().Format(time.RFC3339),
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			// First two attempts → 503
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sampleResp)
	}))
	defer ts.Close()

	client := diagnosis.NewClient(diagnosis.Config{
		Addr:       ts.URL,
		Timeout:    5 * time.Second,
		MaxRetries: 3,
	})

	// Use a very short backoff by re-defining retryBackoffs isn't possible from
	// outside the package, but the test still passes within a few seconds.
	ctx := context.Background()
	inc := contracts.Incident{
		ID:       "test-inc",
		Severity: "critical",
		Signals: []contracts.Signal{{
			ID:       "s1",
			Source:   "prometheus",
			Reason:   "CrashLoopBackOff",
			Severity: "critical",
		}},
		OpenedAt:  time.Now(),
		UpdatedAt: time.Now(),
	}

	diag, err := client.Diagnose(ctx, inc)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diag.FailureMode != "CrashLoopBackOff" {
		t.Errorf("failure_mode: got %q", diag.FailureMode)
	}
	if diag.ProposedAction != "scale-deployment" {
		t.Errorf("proposed_action: got %q", diag.ProposedAction)
	}
	if attempts.Load() < 3 {
		t.Errorf("expected at least 3 attempts (2 failures + 1 success), got %d", attempts.Load())
	}
}

// ---------------------------------------------------------------------------
// Test 4: end-to-end — signal → correlator → orchestrator → gitwriter → bare repo
// ---------------------------------------------------------------------------

// TestEndToEnd_SignalToCommit exercises the full pipeline:
//
//	Signal → Correlator closes incident → Orchestrator pipeline →
//	fake Diagnoser → Policy (propose + mock approval) → gitwriter.Apply → push to bare repo
func TestEndToEnd_SignalToCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	bareDir, workDir := initBareAndClone(t)

	// Fake diagnoser: always returns a scale-deployment diagnosis.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"incident_id":     "e2e-incident",
			"failure_mode":    "CrashLoopBackOff",
			"proposed_action": "scale-deployment",
			"confidence":      0.95,
			"blast_radius":    "deployment",
			"root_cause":      "container crash loop",
			"source":          "test",
			"diagnosed_at":    time.Now().Format(time.RFC3339),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	// GitWriter — RemoteURL points to local bare repo.
	gw := gitwriter.New(gitwriter.Config{
		RepoPath:  workDir,
		BotName:   "autosre-e2e",
		BotEmail:  "e2e@autosre.dev",
		Branch:    "master",
		RemoteURL: bareDir,
	}, slog.Default())

	// Policy engine — permissive config that allows scale-deployment for CrashLoopBackOff.
	// full-auto avoids the approval round-trip which would block in a test.
	polCfg := policy.PolicyConfig{
		DefaultAutonomy:     contracts.AutonomyFullAuto,
		ConfidenceThreshold: 0.50,
		FailureModeRules: map[string]policy.FailureModeRule{
			"CrashLoopBackOff": {
				Autonomy:       contracts.AutonomyFullAuto,
				AllowedActions: []string{"scale-deployment"},
			},
		},
		BlastRadius: policy.BlastRadiusLimits{
			MaxReplicaDelta:     10,
			MaxMemoryBumpFactor: 5.0,
		},
		CircuitBreaker: policy.CircuitBreakerConfig{
			MaxActionsPerWindow: 100,
			WindowSeconds:       300,
		},
	}
	pol := policy.New(polCfg, slog.Default())

	// Diagnosis client.
	diagClient := diagnosis.NewClient(diagnosis.Config{
		Addr:       ts.URL,
		Timeout:    5 * time.Second,
		MaxRetries: 0, // no retries needed for a fake server
	})

	// Orchestrator config: ApplyEnabled=true with tight timeouts.
	orchCfg := config.OrchestratorConfig{
		ApplyEnabled:         true,
		MaxWorkers:           2,
		DefaultContainer:     "app",
		DefaultScaleReplicas: 5,
	}
	builder := orchestrator.NewDefaultBuilder(gw, "app", 5, 1.5, slog.Default())
	orch := orchestrator.New(
		orchCfg,
		diagClient,
		pol,
		&mockNotifier{},
		&mockVerifier{},
		builder,
		audit.NoOp{},
		nil,
		slog.Default(),
	)

	// Correlator with a short resolve window.
	// The resolver ticks every max(ResolveWindow/2, 1s), so we use 2s to get
	// the incident closed ~2s after the last signal.
	cor := correlator.New(correlator.Config{
		CorrelationWindow: 50 * time.Millisecond,
		ResolveWindow:     2 * time.Second,
		DedupWindow:       0,
	}, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	// Start pipeline.
	sigCh := make(chan contracts.Signal, 1)
	go cor.Run(ctx, sigCh)
	go orch.Run(ctx, cor.Events())

	// Send one signal for the payment-service deployment.
	sigCh <- contracts.Signal{
		ID:         "e2e-sig-1",
		Source:     "prometheus-alert",
		Namespace:  "production",
		Resource:   "payment-service",
		Kind:       "Deployment",
		Reason:     "CrashLoopBackOff",
		Severity:   "critical",
		ReceivedAt: time.Now(),
	}

	// Poll the bare repo for 2 commits (initial + remediation).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		if countCommitsInBare(bareDir) >= 2 {
			// Verify the manifest was changed.
			manifestPath := filepath.Join(workDir, "apps", "production", "payment-service.yaml")
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatalf("read manifest after apply: %v", err)
			}
			val, err := gitwriter.GetField(data, "spec.replicas")
			if err != nil {
				t.Fatalf("GetField replicas: %v", err)
			}
			// DefaultScaleReplicas=5 → replicas should now be "5"
			if val != "5" {
				t.Errorf("expected replicas=5, got %q", val)
			}
			return // success
		}
	}
	t.Fatalf("timed out: commit did not appear in bare repo within 10s; bare commits=%d",
		countCommitsInBare(bareDir))
}
