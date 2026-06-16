package gitwriter_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/autosre/agent/internal/gitwriter"
)

// initRepo creates a temporary git repository seeded with one YAML manifest
// and returns its path. The first commit contains the manifest at the given
// relative path with the given content.
func initRepo(t *testing.T, relPath, content string) string {
	t.Helper()
	dir := t.TempDir()

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}

	absPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if _, err := wt.Add(relPath); err != nil {
		t.Fatalf("git add: %v", err)
	}
	sig := &object.Signature{Name: "test", Email: "test@test.com"}
	if _, err := wt.Commit("chore: initial fixture", &git.CommitOptions{Author: sig}); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	return dir
}

const deploymentFixture = `apiVersion: apps/v1
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

func newWriter(t *testing.T, repoPath string) *gitwriter.Writer {
	t.Helper()
	cfg := gitwriter.Config{
		RepoPath: repoPath,
		BotName:  "autosre-bot",
		BotEmail: "bot@autosre.dev",
		Branch:   "main",
	}
	return gitwriter.New(cfg, slog.Default())
}

// ---------------------------------------------------------------------------
// BumpQuantity tests
// ---------------------------------------------------------------------------

func TestBumpQuantity(t *testing.T) {
	cases := []struct {
		input  string
		factor float64
		want   string
	}{
		{"256Mi", 1.5, "384Mi"},
		{"1Gi", 2.0, "2Gi"},
		{"512", 1.5, "768"},
		{"100Mi", 1.0, "100Mi"},
		{"1Ki", 1.5, "2Ki"}, // ceil(1.5) = 2
		{"1000Mi", 1.5, "1500Mi"},
	}
	for _, tc := range cases {
		got, err := gitwriter.BumpQuantity(tc.input, tc.factor)
		if err != nil {
			t.Errorf("BumpQuantity(%q, %g) error: %v", tc.input, tc.factor, err)
			continue
		}
		if got != tc.want {
			t.Errorf("BumpQuantity(%q, %g) = %q; want %q", tc.input, tc.factor, got, tc.want)
		}
	}
}

func TestBumpQuantityErrors(t *testing.T) {
	if _, err := gitwriter.BumpQuantity("", 1.5); err == nil {
		t.Error("expected error for empty quantity")
	}
	if _, err := gitwriter.BumpQuantity("256Mi", 0); err == nil {
		t.Error("expected error for zero factor")
	}
	if _, err := gitwriter.BumpQuantity("notanumber", 1.5); err == nil {
		t.Error("expected error for non-numeric quantity")
	}
}

// ---------------------------------------------------------------------------
// FindManifest tests
// ---------------------------------------------------------------------------

func TestFindManifest_HappyPath(t *testing.T) {
	dir := initRepo(t, "apps/production/payment-service.yaml", deploymentFixture)
	path, err := gitwriter.FindManifest(dir, "production", "Deployment", "payment-service")
	if err != nil {
		t.Fatalf("FindManifest: %v", err)
	}
	if !strings.HasSuffix(path, "payment-service.yaml") {
		t.Errorf("unexpected path: %s", path)
	}
}

func TestFindManifest_NotFound(t *testing.T) {
	dir := initRepo(t, "apps/production/payment-service.yaml", deploymentFixture)
	_, err := gitwriter.FindManifest(dir, "production", "Deployment", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent manifest")
	}
}

// ---------------------------------------------------------------------------
// GetField / SetField tests
// ---------------------------------------------------------------------------

func TestGetField_Replicas(t *testing.T) {
	val, err := gitwriter.GetField([]byte(deploymentFixture), "spec.replicas")
	if err != nil {
		t.Fatalf("GetField: %v", err)
	}
	if val != "3" {
		t.Errorf("replicas: got %q, want %q", val, "3")
	}
}

func TestGetField_Image(t *testing.T) {
	val, err := gitwriter.GetField([]byte(deploymentFixture), "spec.template.spec.containers[name=app].image")
	if err != nil {
		t.Fatalf("GetField: %v", err)
	}
	if val != "myapp/payment-service:v1.4.2" {
		t.Errorf("image: got %q", val)
	}
}

func TestGetField_Memory(t *testing.T) {
	val, err := gitwriter.GetField([]byte(deploymentFixture), "spec.template.spec.containers[name=app].resources.limits.memory")
	if err != nil {
		t.Fatalf("GetField: %v", err)
	}
	if val != "256Mi" {
		t.Errorf("memory: got %q", val)
	}
}

func TestSetField_Replicas(t *testing.T) {
	newData, oldVal, err := gitwriter.SetField([]byte(deploymentFixture), "spec.replicas", "5")
	if err != nil {
		t.Fatalf("SetField: %v", err)
	}
	if oldVal != "3" {
		t.Errorf("oldVal: got %q, want %q", oldVal, "3")
	}
	newVal, err := gitwriter.GetField(newData, "spec.replicas")
	if err != nil {
		t.Fatalf("GetField after set: %v", err)
	}
	if newVal != "5" {
		t.Errorf("newVal: got %q, want %q", newVal, "5")
	}
}

func TestSetField_Image(t *testing.T) {
	newData, oldVal, err := gitwriter.SetField([]byte(deploymentFixture),
		"spec.template.spec.containers[name=app].image", "myapp/payment-service:v1.4.3")
	if err != nil {
		t.Fatalf("SetField: %v", err)
	}
	if oldVal != "myapp/payment-service:v1.4.2" {
		t.Errorf("oldVal: got %q", oldVal)
	}
	newVal, _ := gitwriter.GetField(newData, "spec.template.spec.containers[name=app].image")
	if newVal != "myapp/payment-service:v1.4.3" {
		t.Errorf("newVal: got %q", newVal)
	}
}

// ---------------------------------------------------------------------------
// Writer.EditField integration tests (requires a real git repo)
// ---------------------------------------------------------------------------

func TestEditField_DryRun(t *testing.T) {
	dir := initRepo(t, "apps/production/payment-service.yaml", deploymentFixture)
	w := newWriter(t, dir)

	res, err := w.EditField(context.Background(),
		"production", "payment-service", "Deployment",
		"spec.replicas", "5", true)
	if err != nil {
		t.Fatalf("EditField dry-run: %v", err)
	}
	if !res.DryRun {
		t.Error("expected DryRun=true")
	}
	if res.CommitSHA != "" {
		t.Errorf("expected no commit SHA in dry-run, got %q", res.CommitSHA)
	}
	if res.OldValue != "3" || res.NewValue != "5" {
		t.Errorf("old=%q new=%q", res.OldValue, res.NewValue)
	}
	// File must be unchanged.
	data, _ := os.ReadFile(filepath.Join(dir, "apps/production/payment-service.yaml"))
	val, _ := gitwriter.GetField(data, "spec.replicas")
	if val != "3" {
		t.Errorf("file was mutated during dry-run; replicas=%q", val)
	}
}

func TestEditField_Apply(t *testing.T) {
	dir := initRepo(t, "apps/production/payment-service.yaml", deploymentFixture)
	w := newWriter(t, dir)

	res, err := w.EditField(context.Background(),
		"production", "payment-service", "Deployment",
		"spec.replicas", "5", false)
	if err != nil {
		t.Fatalf("EditField apply: %v", err)
	}
	if res.CommitSHA == "" {
		t.Error("expected non-empty commit SHA")
	}
	// Verify file on disk.
	data, _ := os.ReadFile(filepath.Join(dir, "apps/production/payment-service.yaml"))
	val, _ := gitwriter.GetField(data, "spec.replicas")
	if val != "5" {
		t.Errorf("file not updated; replicas=%q", val)
	}
}

func TestEditField_NoOp(t *testing.T) {
	dir := initRepo(t, "apps/production/payment-service.yaml", deploymentFixture)
	w := newWriter(t, dir)

	res, err := w.EditField(context.Background(),
		"production", "payment-service", "Deployment",
		"spec.replicas", "3", false) // same as current value
	if err != nil {
		t.Fatalf("EditField no-op: %v", err)
	}
	if !res.NoOp {
		t.Error("expected NoOp=true")
	}
	if res.CommitSHA != "" {
		t.Errorf("expected no commit for no-op, got SHA=%q", res.CommitSHA)
	}
}

func TestGetPreviousValue(t *testing.T) {
	dir := initRepo(t, "apps/production/payment-service.yaml", deploymentFixture)
	w := newWriter(t, dir)

	// Commit a change so there is a "previous" value.
	_, err := w.EditField(context.Background(),
		"production", "payment-service", "Deployment",
		"spec.template.spec.containers[name=app].image",
		"myapp/payment-service:v1.4.3", false)
	if err != nil {
		t.Fatalf("EditField: %v", err)
	}

	prev, err := w.GetPreviousValue("production", "payment-service", "Deployment",
		"spec.template.spec.containers[name=app].image")
	if err != nil {
		t.Fatalf("GetPreviousValue: %v", err)
	}
	if prev != "myapp/payment-service:v1.4.2" {
		t.Errorf("previous image: got %q, want %q", prev, "myapp/payment-service:v1.4.2")
	}
}
