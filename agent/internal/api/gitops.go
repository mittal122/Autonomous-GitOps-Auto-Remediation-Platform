package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	gogithttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gogitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/gitwriter"
	"github.com/autosre/agent/internal/settings"
	"github.com/autosre/agent/internal/uid"
)

// GitOpsReloader is the subset of *gitwriter.Writer the API needs to apply
// GitOps settings live, without a process restart.
type GitOpsReloader interface {
	Reload(ctx context.Context, cfg gitwriter.Config) error
}

// ---------------------------------------------------------------------------
// GET /api/v1/integrations/gitops
// ---------------------------------------------------------------------------

type gitOpsIntegrationResponse struct {
	Configured    bool   `json:"configured"`
	RepoPath      string `json:"repo_path,omitempty"`
	RemoteURL     string `json:"remote_url,omitempty"`
	Branch        string `json:"branch,omitempty"`
	BotName       string `json:"bot_name,omitempty"`
	BotEmail      string `json:"bot_email,omitempty"`
	HasAuthToken  bool   `json:"has_auth_token"`
	HasSSHKeyPath bool   `json:"has_ssh_key_path"`
}

func (s *Server) handleGetGitOpsIntegration(w http.ResponseWriter, r *http.Request) error {
	var resp gitOpsIntegrationResponse
	if s.settings != nil {
		if saved, ok, err := s.settings.LoadGitOpsSettings(r.Context()); err == nil && ok {
			resp.Configured = saved.RepoPath != ""
			resp.RepoPath = saved.RepoPath
			resp.RemoteURL = saved.RemoteURL
			resp.Branch = saved.Branch
			resp.BotName = saved.BotName
			resp.BotEmail = saved.BotEmail
			resp.HasAuthToken = saved.AuthToken != ""
			resp.HasSSHKeyPath = saved.SSHKeyPath != ""
		}
	}
	return jsonOK(w, resp)
}

// ---------------------------------------------------------------------------
// POST /api/v1/integrations/gitops
// ---------------------------------------------------------------------------

// gitOpsRequest is shared by the save and test-connection endpoints.
// AuthToken/SSHKeyPath are pointers so the save endpoint can distinguish
// "field omitted — keep the existing saved value" from "field present and
// empty — clear it".
type gitOpsRequest struct {
	RepoPath   string  `json:"repo_path"`
	RemoteURL  string  `json:"remote_url"`
	AuthToken  *string `json:"auth_token"`
	SSHKeyPath *string `json:"ssh_key_path"`
	BotName    string  `json:"bot_name"`
	BotEmail   string  `json:"bot_email"`
	Branch     string  `json:"branch"`
}

func (s *Server) handleSaveGitOpsIntegration(w http.ResponseWriter, r *http.Request) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	var req gitOpsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return &apiError{"invalid JSON", http.StatusBadRequest}
	}
	if req.RepoPath == "" {
		return &apiError{"repo_path is required", http.StatusBadRequest}
	}

	if s.settings == nil {
		return &apiError{"persistence unavailable; cannot save settings", http.StatusServiceUnavailable}
	}

	existing, _, _ := s.settings.LoadGitOpsSettings(r.Context())
	resolve := func(req *string, existing string) string {
		if req != nil {
			return *req
		}
		return existing
	}

	branch := req.Branch
	if branch == "" {
		branch = "main"
	}
	botName := req.BotName
	if botName == "" {
		botName = "autosre-bot"
	}
	botEmail := req.BotEmail
	if botEmail == "" {
		botEmail = "autosre-bot@localhost"
	}

	saved := settings.GitOpsSettings{
		RepoPath:   req.RepoPath,
		RemoteURL:  req.RemoteURL,
		AuthToken:  resolve(req.AuthToken, existing.AuthToken),
		SSHKeyPath: resolve(req.SSHKeyPath, existing.SSHKeyPath),
		BotName:    botName,
		BotEmail:   botEmail,
		Branch:     branch,
	}
	if err := s.settings.SaveGitOpsSettings(r.Context(), saved); err != nil {
		s.log.Warn("api: save gitops settings failed", "error", err)
		return &apiError{"failed to save settings", http.StatusInternalServerError}
	}

	if s.gitops != nil {
		if err := s.gitops.Reload(r.Context(), gitwriter.Config{
			RepoPath:   saved.RepoPath,
			BotName:    saved.BotName,
			BotEmail:   saved.BotEmail,
			Branch:     saved.Branch,
			RemoteURL:  saved.RemoteURL,
			AuthToken:  saved.AuthToken,
			SSHKeyPath: saved.SSHKeyPath,
		}); err != nil {
			s.log.Warn("api: reload gitwriter failed", "error", err)
			return &apiError{"settings saved, but failed to apply live: " + err.Error(), http.StatusInternalServerError}
		}
	}

	s.record(r.Context(), uid.New(), "integrations", audit.Stage("IntegrationConfigured"), "gitops-saved", map[string]string{
		"repo_path": req.RepoPath,
		"branch":    branch,
		"source":    "web-api",
	})

	return jsonOK(w, map[string]any{"saved": true, "repo_path": req.RepoPath})
}

// ---------------------------------------------------------------------------
// POST /api/v1/integrations/gitops/test
// ---------------------------------------------------------------------------

type gitOpsTestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func (s *Server) handleTestGitOpsIntegration(w http.ResponseWriter, r *http.Request) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	var req gitOpsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return &apiError{"invalid JSON", http.StatusBadRequest}
	}
	if req.RemoteURL == "" {
		return &apiError{"remote_url is required to test push access", http.StatusBadRequest}
	}

	authToken := ""
	if req.AuthToken != nil {
		authToken = *req.AuthToken
	}
	sshKeyPath := ""
	if req.SSHKeyPath != nil {
		sshKeyPath = *req.SSHKeyPath
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	return jsonOK(w, testGitRemote(ctx, req.RemoteURL, authToken, sshKeyPath))
}

// testGitRemote performs a read-only ls-remote against remoteURL using an
// in-memory storer — it never touches the local filesystem and never writes
// anything, just confirms the credentials can list refs on the remote.
func testGitRemote(ctx context.Context, remoteURL, authToken, sshKeyPath string) gitOpsTestResult {
	remote := git.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteURL},
	})

	opts := &git.ListOptions{}
	switch {
	case authToken != "":
		opts.Auth = &gogithttp.BasicAuth{Username: "x-token", Password: authToken}
	case sshKeyPath != "":
		auth, err := gogitssh.NewPublicKeysFromFile("git", sshKeyPath, "")
		if err != nil {
			return gitOpsTestResult{OK: false, Message: fmt.Sprintf("cannot load SSH key: %v", err)}
		}
		opts.Auth = auth
	}

	refs, err := remote.ListContext(ctx, opts)
	if err != nil {
		return gitOpsTestResult{OK: false, Message: fmt.Sprintf("cannot reach remote: %v", err)}
	}

	return gitOpsTestResult{OK: true, Message: fmt.Sprintf("Connected — found %d ref(s) on the remote", len(refs))}
}
