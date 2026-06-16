package remediator_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/autosre/agent/internal/gitwriter"
	"github.com/autosre/agent/internal/remediator"
)

const fixture = `apiVersion: apps/v1
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

func initRepo(t *testing.T) (repoPath string, w *gitwriter.Writer) {
	t.Helper()
	dir := t.TempDir()

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}

	absPath := filepath.Join(dir, "apps/production/payment-service.yaml")
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(absPath, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	wt, _ := repo.Worktree()
	if _, err := wt.Add("apps/production/payment-service.yaml"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	sig := &object.Signature{Name: "test", Email: "test@test.com"}
	if _, err := wt.Commit("chore: initial", &gogit.CommitOptions{Author: sig}); err != nil {
		t.Fatalf("initial commit: %v", err)
	}

	cfg := gitwriter.Config{
		RepoPath: dir, BotName: "bot", BotEmail: "bot@test.com", Branch: "main",
	}
	return dir, gitwriter.New(cfg, slog.Default())
}

// ---------------------------------------------------------------------------
// ScaleDeployment
// ---------------------------------------------------------------------------

func TestScaleDeployment_DryRun(t *testing.T) {
	_, w := initRepo(t)
	action := remediator.NewScaleDeployment(w, "production", "payment-service", 5, true, slog.Default())

	desc, err := action.DryRun(context.Background())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if desc == "" {
		t.Error("expected non-empty description")
	}
}

func TestScaleDeployment_ApplyAndRollback(t *testing.T) {
	dir, w := initRepo(t)
	action := remediator.NewScaleDeployment(w, "production", "payment-service", 5, false, slog.Default())

	if err := action.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "apps/production/payment-service.yaml"))
	val, _ := gitwriter.GetField(data, "spec.replicas")
	if val != "5" {
		t.Errorf("after Apply: replicas=%q, want 5", val)
	}

	if err := action.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	data, _ = os.ReadFile(filepath.Join(dir, "apps/production/payment-service.yaml"))
	val, _ = gitwriter.GetField(data, "spec.replicas")
	if val != "3" {
		t.Errorf("after Rollback: replicas=%q, want 3", val)
	}
}

func TestScaleDeployment_NoOp(t *testing.T) {
	_, w := initRepo(t)
	action := remediator.NewScaleDeployment(w, "production", "payment-service", 3, false, slog.Default())

	if err := action.Apply(context.Background()); err != nil {
		t.Fatalf("Apply no-op: %v", err)
	}
	// Rollback of a no-op should succeed silently.
	if err := action.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback of no-op: %v", err)
	}
}

// ---------------------------------------------------------------------------
// RollbackDeployment
// ---------------------------------------------------------------------------

func TestRollbackDeployment_KnownGoodRef(t *testing.T) {
	dir, w := initRepo(t)
	action := remediator.NewRollbackDeployment(
		w, "production", "payment-service", "app", "myapp/payment-service:v1.4.1", false, slog.Default())

	if err := action.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "apps/production/payment-service.yaml"))
	val, _ := gitwriter.GetField(data, "spec.template.spec.containers[name=app].image")
	if val != "myapp/payment-service:v1.4.1" {
		t.Errorf("image=%q, want v1.4.1", val)
	}

	if err := action.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "apps/production/payment-service.yaml"))
	val, _ = gitwriter.GetField(data, "spec.template.spec.containers[name=app].image")
	if val != "myapp/payment-service:v1.4.2" {
		t.Errorf("after rollback image=%q, want v1.4.2", val)
	}
}

func TestRollbackDeployment_DryRun(t *testing.T) {
	_, w := initRepo(t)
	action := remediator.NewRollbackDeployment(
		w, "production", "payment-service", "app", "myapp/payment-service:v1.4.1", true, slog.Default())

	desc, err := action.DryRun(context.Background())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if desc == "" {
		t.Error("expected non-empty description")
	}
}

// ---------------------------------------------------------------------------
// BumpMemoryLimit
// ---------------------------------------------------------------------------

func TestBumpMemoryLimit_ApplyAndRollback(t *testing.T) {
	dir, w := initRepo(t)
	action := remediator.NewBumpMemoryLimit(
		w, "production", "payment-service", "app", 1.5, false, slog.Default())

	if err := action.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "apps/production/payment-service.yaml"))
	val, _ := gitwriter.GetField(data, "spec.template.spec.containers[name=app].resources.limits.memory")
	if val != "384Mi" {
		t.Errorf("memory limit=%q, want 384Mi", val)
	}

	if err := action.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "apps/production/payment-service.yaml"))
	val, _ = gitwriter.GetField(data, "spec.template.spec.containers[name=app].resources.limits.memory")
	if val != "256Mi" {
		t.Errorf("after rollback memory=%q, want 256Mi", val)
	}
}

func TestBumpMemoryLimit_DryRun(t *testing.T) {
	_, w := initRepo(t)
	action := remediator.NewBumpMemoryLimit(
		w, "production", "payment-service", "app", 1.5, true, slog.Default())

	desc, err := action.DryRun(context.Background())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if desc == "" {
		t.Error("expected non-empty description")
	}
}

func TestBumpMemoryLimit_DefaultFactor(t *testing.T) {
	dir, w := initRepo(t)
	// factor=0 should fall back to 1.5
	action := remediator.NewBumpMemoryLimit(
		w, "production", "payment-service", "app", 0, false, slog.Default())

	if err := action.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "apps/production/payment-service.yaml"))
	val, _ := gitwriter.GetField(data, "spec.template.spec.containers[name=app].resources.limits.memory")
	if val != "384Mi" {
		t.Errorf("default factor: memory=%q, want 384Mi", val)
	}
}
