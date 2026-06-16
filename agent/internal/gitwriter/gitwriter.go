package gitwriter

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Config holds settings for the shared git writer.
type Config struct {
	RepoPath string // absolute path to the local gitops repository clone
	BotName  string // git commit author name
	BotEmail string // git commit author email
	Branch   string // branch to commit on (e.g. "main")
}

// Result is returned by EditField and carries everything the caller needs
// to display a diff, record audit state, and later perform a rollback.
type Result struct {
	File      string // repo-relative path to the manifest
	Field     string // field path that was changed
	OldValue  string // value before the edit
	NewValue  string // value after the edit
	CommitSHA string // empty in dry-run mode
	DryRun    bool
	NoOp      bool   // true when OldValue == NewValue
	Diff      string // human-readable diff
}

// Writer is the shared engine for structure-preserving YAML edits committed
// via go-git. It never calls the Kubernetes API.
type Writer struct {
	cfg Config
	log *slog.Logger
}

// New creates a Writer. The caller is responsible for ensuring cfg.RepoPath
// points to a valid git repository before calling EditField.
func New(cfg Config, log *slog.Logger) *Writer {
	return &Writer{cfg: cfg, log: log}
}

// GetCurrentValue returns the current value of fieldPath in the manifest for
// (namespace, kind, name) without making any changes.
func (w *Writer) GetCurrentValue(namespace, name, kind, fieldPath string) (string, error) {
	absPath, err := FindManifest(w.cfg.RepoPath, namespace, kind, name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", absPath, err)
	}
	return GetField(data, fieldPath)
}

// GetPreviousValue returns the value of fieldPath as it existed in the commit
// before the most recent change to the manifest file. Useful for RollbackDeployment
// to discover the last-known-good image tag without requiring it to be supplied
// by the caller.
func (w *Writer) GetPreviousValue(namespace, name, kind, fieldPath string) (string, error) {
	absPath, err := FindManifest(w.cfg.RepoPath, namespace, kind, name)
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(w.cfg.RepoPath, absPath)
	if err != nil {
		return "", fmt.Errorf("rel path: %w", err)
	}
	relPath = filepath.ToSlash(relPath)

	repo, err := git.PlainOpen(w.cfg.RepoPath)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}

	logOpts := &git.LogOptions{
		PathFilter: func(path string) bool { return path == relPath },
	}
	iter, err := repo.Log(logOpts)
	if err != nil {
		return "", fmt.Errorf("git log: %w", err)
	}
	defer iter.Close()

	// Skip the most recent commit; return the value from the second one.
	var count int
	var prevValue string
	err = iter.ForEach(func(c *object.Commit) error {
		count++
		if count < 2 {
			return nil
		}
		f, treeErr := c.File(relPath)
		if treeErr != nil {
			return treeErr
		}
		contents, readErr := f.Contents()
		if readErr != nil {
			return readErr
		}
		val, getErr := GetField([]byte(contents), fieldPath)
		if getErr != nil {
			return getErr
		}
		prevValue = val
		return fmt.Errorf("stop") // sentinel to break iteration
	})
	if err != nil && err.Error() != "stop" {
		return "", fmt.Errorf("walking git log for %s: %w", relPath, err)
	}
	if count < 2 {
		return "", fmt.Errorf("no previous commit found for %s", relPath)
	}
	return prevValue, nil
}

// EditField finds the manifest, edits fieldPath to newValue, and either
// returns a dry-run diff or commits the change to the git repo.
func (w *Writer) EditField(
	ctx context.Context,
	namespace, name, kind, fieldPath, newValue string,
	dryRun bool,
) (Result, error) {
	absPath, err := FindManifest(w.cfg.RepoPath, namespace, kind, name)
	if err != nil {
		return Result{}, err
	}
	relPath, err := filepath.Rel(w.cfg.RepoPath, absPath)
	if err != nil {
		return Result{}, fmt.Errorf("rel path: %w", err)
	}
	relPath = filepath.ToSlash(relPath)

	data, err := os.ReadFile(absPath)
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", absPath, err)
	}

	newData, oldValue, err := SetField(data, fieldPath, newValue)
	if err != nil {
		return Result{}, err
	}

	diff := generateDiff(relPath, fieldPath, oldValue, newValue)
	res := Result{
		File:     relPath,
		Field:    fieldPath,
		OldValue: oldValue,
		NewValue: newValue,
		DryRun:   dryRun,
		NoOp:     oldValue == newValue,
		Diff:     diff,
	}

	if res.NoOp {
		w.log.InfoContext(ctx, "gitwriter: no-op — field already has target value",
			"file", relPath, "field", fieldPath, "value", newValue)
		return res, nil
	}

	if dryRun {
		w.log.InfoContext(ctx, "gitwriter: dry-run diff", "file", relPath, "field", fieldPath,
			"old", oldValue, "new", newValue)
		return res, nil
	}

	sha, err := w.commit(ctx, absPath, relPath, newData, fieldPath, oldValue, newValue)
	if err != nil {
		return Result{}, err
	}
	res.CommitSHA = sha
	w.log.InfoContext(ctx, "gitwriter: committed", "sha", sha, "file", relPath,
		"field", fieldPath, "old", oldValue, "new", newValue)
	return res, nil
}

// commit writes newData to absPath and creates a git commit. If the commit
// fails, the original file content is restored before returning the error.
func (w *Writer) commit(
	ctx context.Context,
	absPath, relPath string,
	newData []byte,
	field, oldValue, newValue string,
) (string, error) {
	// Snapshot the original content for rollback on error.
	original, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("snapshot original: %w", err)
	}

	// Write the edited content.
	if err := os.WriteFile(absPath, newData, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", absPath, err)
	}

	repo, err := git.PlainOpen(w.cfg.RepoPath)
	if err != nil {
		_ = os.WriteFile(absPath, original, 0o644)
		return "", fmt.Errorf("open repo: %w", err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		_ = os.WriteFile(absPath, original, 0o644)
		return "", fmt.Errorf("worktree: %w", err)
	}

	if _, err := wt.Add(relPath); err != nil {
		_ = os.WriteFile(absPath, original, 0o644)
		return "", fmt.Errorf("git add %s: %w", relPath, err)
	}

	msg := buildCommitMessage(relPath, field, oldValue, newValue)
	hash, err := wt.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{
			Name:  w.cfg.BotName,
			Email: w.cfg.BotEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		_ = os.WriteFile(absPath, original, 0o644)
		return "", fmt.Errorf("git commit: %w", err)
	}

	return hash.String(), nil
}

// buildCommitMessage produces a conventional-commit message for an automated edit.
func buildCommitMessage(relPath, field, oldValue, newValue string) string {
	// Extract a short resource name from the file path for the scope.
	base := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
	return fmt.Sprintf("fix(%s): set %s from %s to %s\n\nAutomated GitOps remediation by autosre-agent.\nFile: %s\n",
		base, field, oldValue, newValue, relPath)
}
