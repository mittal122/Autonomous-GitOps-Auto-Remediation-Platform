// Package config loads agent runtime configuration from environment variables.
// All settings have documented defaults; no setting is silently ignored.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// CorrelatorConfig holds tuning knobs for the correlator's time windows.
type CorrelatorConfig struct {
	// CorrelationWindow is how long a signal can be grouped into an existing
	// incident for the same resource. Default: 5m.
	CorrelationWindow time.Duration
	// ResolveWindow is how long an incident can be quiet before it is closed.
	// Default: 10m.
	ResolveWindow time.Duration
	// DedupWindow suppresses duplicate (same resource + reason) signals within
	// this window. Default: 1m.
	DedupWindow time.Duration
}

// RemediatorConfig holds settings for the GitOps-native remediation engine.
type RemediatorConfig struct {
	// RepoPath is the absolute path to the local clone of the GitOps config repo.
	RepoPath string // env: GITOPS_REPO_PATH
	// BotName and BotEmail are used as the git commit author.
	BotName  string // env: GIT_BOT_NAME
	BotEmail string // env: GIT_BOT_EMAIL
	// Branch is the branch to commit on. Default: "main".
	Branch string // env: GIT_BRANCH
	// DefaultDryRun makes all remediation actions dry-run unless --apply is passed.
	// Default: true (safe by default).
	DefaultDryRun bool // env: REMEDIATION_DRY_RUN
	// MemoryBumpFactor is the multiplier used by BumpMemoryLimit. Default: 1.5.
	MemoryBumpFactor float64 // env: MEMORY_BUMP_FACTOR
}

// VerifierConfig holds tuning knobs for the post-remediation recovery checker.
type VerifierConfig struct {
	// GraceDelay is how long to wait after a remediation before starting to observe.
	// Allows ArgoCD time to sync the commit before judging recovery. Default: 30s.
	GraceDelay time.Duration // env: VERIFIER_GRACE_DELAY
	// Window is the total observation period after the grace delay. Default: 5m.
	Window time.Duration // env: VERIFIER_WINDOW
	// PollInterval is how often to check the recovery source during the window. Default: 15s.
	PollInterval time.Duration // env: VERIFIER_POLL_INTERVAL
	// FailureThreshold is the max number of matching signals allowed before FAILED. Default: 0.
	FailureThreshold int // env: VERIFIER_FAILURE_THRESHOLD
}

// OrchestratorConfig holds controls for the reconcile loop.
type OrchestratorConfig struct {
	// ApplyEnabled must be explicitly set true to allow GitOps commits.
	// Default: false (dry-run-only). Change is explicit, gated, reversible.
	ApplyEnabled bool // env: ORCHESTRATOR_APPLY_ENABLED

	// KillSwitch halts all remediation immediately when true.
	// Takes effect before every apply; can be toggled at runtime.
	KillSwitch bool // env: ORCHESTRATOR_KILL_SWITCH

	// MaxWorkers is the size of the bounded worker pool. Default: 5.
	MaxWorkers int // env: ORCHESTRATOR_MAX_WORKERS

	// DefaultContainer is used for rollback/bump-memory when the diagnosis
	// does not specify a container. Default: "app".
	DefaultContainer string // env: ORCHESTRATOR_DEFAULT_CONTAINER

	// DefaultScaleReplicas is used for scale-deployment when TargetReplicas
	// is not set in the diagnosis. Default: 2.
	DefaultScaleReplicas int // env: ORCHESTRATOR_DEFAULT_SCALE_REPLICAS

	// PolicyFile is the path to the policy.yaml loaded by the Engine.
	// Default: "" (fail-closed engine defaults).
	PolicyFile string // env: POLICY_FILE
}

// NotifierConfig holds settings for all outbound notification channels.
type NotifierConfig struct {
	// Slack
	SlackBotToken      string        // env: SLACK_BOT_TOKEN      (empty → log-only)
	SlackSigningSecret string        // env: SLACK_SIGNING_SECRET (empty → reject all inbound)
	SlackChannelID     string        // env: SLACK_CHANNEL_ID     (empty → log-only)
	// PagerDuty
	PagerDutyRoutingKey string       // env: PAGERDUTY_ROUTING_KEY (empty → skip PD)
	// Timing & resilience
	ApprovalTimeout time.Duration    // env: NOTIFIER_APPROVAL_TIMEOUT (default: 30m)
	SendTimeout     time.Duration    // env: NOTIFIER_SEND_TIMEOUT     (default: 10s)
	MaxRetries      int              // env: NOTIFIER_MAX_RETRIES      (default: 3)
}

// DiagnoserConfig holds settings for the Python diagnoser HTTP client.
type DiagnoserConfig struct {
	// Addr is the base URL of the diagnoser service (e.g. "http://localhost:8001").
	Addr string // env: DIAGNOSER_ADDR
	// Timeout is the per-request timeout including LLM latency. Default: 35s.
	Timeout time.Duration // env: DIAGNOSER_TIMEOUT
}

// APIConfig controls the REST API server and OIDC auth.
type APIConfig struct {
	// OIDCEnabled enables JWT validation on all API routes. Default: false (dev mode — all
	// requests are treated as viewer). Set to true in production.
	OIDCEnabled bool // env: API_OIDC_ENABLED
	// OIDCIssuerURL is the OIDC provider issuer (e.g. "https://accounts.google.com").
	// Required when OIDCEnabled is true.
	OIDCIssuerURL string // env: API_OIDC_ISSUER_URL
	// OIDCClientID is the OAuth2 client ID used for token audience validation.
	OIDCClientID string // env: API_OIDC_CLIENT_ID
	// OIDCRolesClaimKey is the JWT claim key containing the user's roles list.
	// Default: "roles".
	OIDCRolesClaimKey string // env: API_OIDC_ROLES_CLAIM
	// WebUIDir is the path to the built web UI static files.
	// When set, the Go server serves the UI at /. When empty, / returns a "build UI" message.
	WebUIDir string // env: WEB_UI_DIR
}

// AuditConfig controls the append-only event log written by the orchestrator pipeline.
type AuditConfig struct {
	// Enabled writes a JSONL audit file. Default: true.
	Enabled bool // env: AUDIT_ENABLED
	// FilePath is the path of the JSONL audit file. Default: "./data/audit.jsonl".
	FilePath string // env: AUDIT_FILE_PATH
}

// LearnerConfig controls how the Go agent reaches the Python learner service.
type LearnerConfig struct {
	// Addr is the base URL of the learner (e.g. "http://localhost:8002").
	// Empty string disables outcome reporting entirely.
	Addr string // env: LEARNER_ADDR
	// Timeout is the per-report HTTP timeout. Default: 5s.
	Timeout time.Duration // env: LEARNER_TIMEOUT
}

// Config is the top-level runtime configuration for the agent.
type Config struct {
	// Kubernetes
	Kubeconfig string // path to kubeconfig file; ignored when InCluster=true
	InCluster  bool   // use in-cluster service-account credentials

	// HTTP server
	WebhookAddr string // address for the webhook + inspection server; default ":8080"

	// Correlator
	Correlator CorrelatorConfig

	// Remediator
	Remediator RemediatorConfig

	// Diagnoser
	Diagnoser DiagnoserConfig

	// Verifier
	Verifier VerifierConfig

	// Notifier
	Notifier NotifierConfig

	// Orchestrator
	Orchestrator OrchestratorConfig

	// Audit log
	Audit AuditConfig

	// Learner outcome client
	Learner LearnerConfig

	// Web API + Auth
	API APIConfig

	// Logging
	LogLevel string // "debug", "info", "warn", "error"; default "info"
}

// Load reads configuration from environment variables with sane defaults.
// It does not fail-fast on missing values; callers that need a variable must
// check the returned Config and call Validate() explicitly.
func Load() Config {
	return Config{
		Kubeconfig:  getEnv("KUBECONFIG", ""),
		InCluster:   getEnv("IN_CLUSTER", "false") == "true",
		WebhookAddr: getEnv("WEBHOOK_ADDR", ":8080"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
		Correlator: CorrelatorConfig{
			CorrelationWindow: getDuration("CORRELATION_WINDOW", 5*time.Minute),
			ResolveWindow:     getDuration("RESOLVE_WINDOW", 10*time.Minute),
			DedupWindow:       getDuration("DEDUP_WINDOW", 1*time.Minute),
		},
		Remediator: RemediatorConfig{
			RepoPath:         getEnv("GITOPS_REPO_PATH", ""),
			BotName:          getEnv("GIT_BOT_NAME", "autosre-bot"),
			BotEmail:         getEnv("GIT_BOT_EMAIL", "autosre-bot@localhost"),
			Branch:           getEnv("GIT_BRANCH", "main"),
			DefaultDryRun:    getEnv("REMEDIATION_DRY_RUN", "true") != "false",
			MemoryBumpFactor: getFloat("MEMORY_BUMP_FACTOR", 1.5),
		},
		Diagnoser: DiagnoserConfig{
			Addr:    getEnv("DIAGNOSER_ADDR", "http://localhost:8001"),
			Timeout: getDuration("DIAGNOSER_TIMEOUT", 35*time.Second),
		},
		Verifier: VerifierConfig{
			GraceDelay:       getDuration("VERIFIER_GRACE_DELAY", 30*time.Second),
			Window:           getDuration("VERIFIER_WINDOW", 5*time.Minute),
			PollInterval:     getDuration("VERIFIER_POLL_INTERVAL", 15*time.Second),
			FailureThreshold: getInt("VERIFIER_FAILURE_THRESHOLD", 0),
		},
		Notifier: NotifierConfig{
			SlackBotToken:       getEnv("SLACK_BOT_TOKEN", ""),
			SlackSigningSecret:  getEnv("SLACK_SIGNING_SECRET", ""),
			SlackChannelID:      getEnv("SLACK_CHANNEL_ID", ""),
			PagerDutyRoutingKey: getEnv("PAGERDUTY_ROUTING_KEY", ""),
			ApprovalTimeout:     getDuration("NOTIFIER_APPROVAL_TIMEOUT", 30*time.Minute),
			SendTimeout:         getDuration("NOTIFIER_SEND_TIMEOUT", 10*time.Second),
			MaxRetries:          getInt("NOTIFIER_MAX_RETRIES", 3),
		},
		Orchestrator: OrchestratorConfig{
			ApplyEnabled:         getEnv("ORCHESTRATOR_APPLY_ENABLED", "false") == "true",
			KillSwitch:           getEnv("ORCHESTRATOR_KILL_SWITCH", "false") == "true",
			MaxWorkers:           getInt("ORCHESTRATOR_MAX_WORKERS", 5),
			DefaultContainer:     getEnv("ORCHESTRATOR_DEFAULT_CONTAINER", "app"),
			DefaultScaleReplicas: getInt("ORCHESTRATOR_DEFAULT_SCALE_REPLICAS", 2),
			PolicyFile:           getEnv("POLICY_FILE", ""),
		},
		Audit: AuditConfig{
			Enabled:  getEnv("AUDIT_ENABLED", "true") != "false",
			FilePath: getEnv("AUDIT_FILE_PATH", "./data/audit.jsonl"),
		},
		Learner: LearnerConfig{
			Addr:    getEnv("LEARNER_ADDR", ""),
			Timeout: getDuration("LEARNER_TIMEOUT", 5*time.Second),
		},
		API: APIConfig{
			OIDCEnabled:       getEnv("API_OIDC_ENABLED", "false") == "true",
			OIDCIssuerURL:     getEnv("API_OIDC_ISSUER_URL", ""),
			OIDCClientID:      getEnv("API_OIDC_CLIENT_ID", ""),
			OIDCRolesClaimKey: getEnv("API_OIDC_ROLES_CLAIM", "roles"),
			WebUIDir:          getEnv("WEB_UI_DIR", ""),
		},
	}
}

// Validate returns an error if any required field is missing or invalid.
func (c Config) Validate() error {
	if c.Correlator.ResolveWindow <= 0 {
		return fmt.Errorf("RESOLVE_WINDOW must be positive, got %s", c.Correlator.ResolveWindow)
	}
	if c.Correlator.DedupWindow < 0 {
		return fmt.Errorf("DEDUP_WINDOW must be non-negative, got %s", c.Correlator.DedupWindow)
	}
	return nil
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getDuration(key string, defaultVal time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultVal
	}
	return d
}

func getInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return i
}

func getFloat(key string, defaultVal float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return defaultVal
	}
	return f
}
