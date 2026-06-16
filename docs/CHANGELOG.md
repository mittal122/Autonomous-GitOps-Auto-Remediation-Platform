# Changelog

---

## Prompt 9 — Web UI + Hardening (2026-06-16)

### Features Added
- **`internal/api`** — read-first REST API server (`Server`, `NewServer`, `Handler`); all GET endpoints read-only; write endpoints gated by role and audited; `CORS` on all responses; static Web UI serving via `WEB_UI_DIR` env var.
- **RBAC/OIDC auth middleware** (`internal/api/auth.go`) — `Role` type (`viewer`/`operator`/`admin`); `enforce(next, minRole)` wrapper; dev mode (OIDC disabled) grants viewer to all; OIDC mode extracts roles claim from JWT; fails closed (missing token → 401, insufficient role → 403). TODO: swap base64 decode for `coreos/go-oidc` cryptographic verification in production.
- **Approval API** — `POST /api/v1/approvals/{id}/approve` and `/reject` route through existing fail-closed `CompositeNotifier.ResolveApproval()`. No new path to cluster writes.
- **Kill-switch API** — `POST /api/v1/control/kill-switch` (admin only); calls `orchestrator.SetKillSwitch()`; every toggle audited with `KillSwitchToggled` stage, operator identity, and reason.
- **Status API** — `GET /api/v1/status` returns apply_enabled, kill_switch_engaged, in_flight_pipelines, circuit_breaker_{tripped,count,max,window_sec}.
- **Trace API** — `GET /api/v1/incidents/{id}/trace` queries audit sink with QueryFilter; supports trace_id, stage, limit query params.
- **Stats proxy** — `GET /api/v1/stats` proxies to learner `/stats`; advisory-only.
- **Policy engine getters** — `CircuitBreakerTripped()`, `CircuitBreakerCount()`, `CircuitBreakerMax()`, `CircuitBreakerWindowSeconds()` on `*policy.Engine`.
- **Orchestrator getters** — `ApplyEnabled() bool`, `KillSwitchEngaged() bool`, `InFlightCount() int` on `*orchestrator.Orchestrator`.
- **Notifier registry extensions** — `PendingApproval` exported type; `pendingEntry` stores `proposal`+`requestedAt`; `registry.list()` method; `CompositeNotifier.ListPendingApprovals()` + `ResolveApproval()`.
- **`internal/api/api_test.go`** — 18 tests covering RBAC enforcement, read endpoints, write endpoints, audit recording, nil-sink safety.
- **Web UI** (`web-ui/`) — React 18 + TypeScript + Vite + Tailwind; 5 pages: Dashboard (incidents table), IncidentTrace (audit event viewer with expandable details), Approvals (approve/reject UI), Stats (success rates), Status (system status + kill-switch admin toggle); typed API client; Vite dev proxy to `:8080`.
- **Chaos tests** (`agent/tests/chaos/chaos_test.go`) — 4 pure-Go tests; synthetic incidents for all 6 failure modes; validates audit trail stages; apply never enabled; kill-switch halt test; bad-diagnosis panic safety.
- **Helm chart** (`charts/autosre/`) — full chart for agent + diagnoser + learner; ServiceAccount + RBAC; policy ConfigMap; PVCs for audit log + outcome data; NOTES.txt; values for OIDC, apply flag, kill switch, learner addr.
- **ArgoCD Application** (`gitops/autosre-app.yaml`) — automated sync with selfHeal; ignoreDifferences for in-cluster env overrides (kill switch); CreateNamespace.
- **`APIConfig`** in `internal/config` — `OIDCEnabled`, `OIDCIssuerURL`, `OIDCClientID`, `OIDCRolesClaimKey`, `WebUIDir`; env vars documented in `.env.example`.
- **`cmd/autosre/run.go` updated** — imports `internal/api`; creates `api.NewServer()` and mounts its handler at `/` (catch-all after specific routes).
- **Operator runbook** (`docs/OPERATOR_RUNBOOK.md`) — how to deploy, configure RBAC, read audit traces, approve remediations, use kill switch, enable apply mode, troubleshoot.

### Safety Invariants
- **No new path to cluster write** — API write endpoints route through existing safety gates (approval registry, kill switch, policy engine). No endpoint calls remediator, gitwriter, or k8s API directly.
- **Auth fails closed** — missing/invalid token → 401; insufficient role → 403. Dev mode (OIDC disabled) is loudly warned and grants viewer only.
- **Kill-switch toggle requires admin + reason** — audited with operator identity; bad JSON → 400.
- **Chaos tests always dry-run** — `applyEnabled: false` is asserted in `TestChaos_ApplyNeverEnabled`; `StageApplied` audit event presence is checked and must be absent.
- **Non-fatal audit in API layer** — `s.record()` logs and continues on sink errors; nil sink does not panic (tested).

### Files Created
- `agent/internal/api/auth.go` — RBAC middleware
- `agent/internal/api/api.go` — API server (18 endpoints)
- `agent/internal/api/api_test.go` — 18 tests
- `agent/tests/chaos/chaos_test.go` — 4 chaos tests
- `web-ui/package.json`, `vite.config.ts`, `tsconfig.json`, `tsconfig.node.json`
- `web-ui/tailwind.config.js`, `postcss.config.js`, `index.html`
- `web-ui/src/main.tsx`, `src/index.css`, `src/App.tsx`
- `web-ui/src/api/client.ts`
- `web-ui/src/components/Layout.tsx`
- `web-ui/src/pages/Dashboard.tsx`, `IncidentTrace.tsx`, `Approvals.tsx`, `Stats.tsx`, `Status.tsx`
- `charts/autosre/Chart.yaml`, `values.yaml`
- `charts/autosre/templates/_helpers.tpl`, `serviceaccount.yaml`, `rbac.yaml`
- `charts/autosre/templates/configmap.yaml`, `deployment-agent.yaml`, `deployment-diagnoser.yaml`, `deployment-learner.yaml`, `service.yaml`, `NOTES.txt`
- `gitops/autosre-app.yaml` — ArgoCD Application
- `docs/OPERATOR_RUNBOOK.md`

### Files Modified
- `agent/internal/policy/engine.go` — added 4 circuit-breaker getter methods
- `agent/internal/orchestrator/orchestrator.go` — added `ApplyEnabled()`, `KillSwitchEngaged()`, `InFlightCount()` getters
- `agent/internal/notifier/registry.go` — `PendingApproval` type, `pendingEntry` extended, `list()` method
- `agent/internal/notifier/slack.go` — `register()` call updated to pass `proposal`
- `agent/internal/notifier/composite.go` — `ListPendingApprovals()`, `ResolveApproval()` added
- `agent/internal/config/config.go` — `APIConfig` struct + `API` field + Load() wiring
- `agent/cmd/autosre/run.go` — import api, create `apiSrv`, mount `Handler()` at `/`
- `.env.example` — `API_OIDC_ENABLED`, `API_OIDC_ISSUER_URL`, `API_OIDC_CLIENT_ID`, `API_OIDC_ROLES_CLAIM`, `WEB_UI_DIR`
- `docs/PROJECT_PROGRESS.md` — marked project feature-complete

---

## Prompt 8 — Audit + Learning (2026-06-16)

### Features Added
- **`internal/audit`** — append-only `AuditSink` interface (`Record` + `Query` only; no Update/Delete); `AuditEvent` type with `TraceID`/`IncidentID`/`Stage`/`Outcome`/`Details`; `FileSink` (JSONL, append mode), `MemorySink` (goroutine-safe, in-memory), `NoOp` (discard); `QueryFilter` with IncidentID/TraceID/Stage/Since/Until/Limit.
- **`internal/outcome`** — `Record` struct (all pipeline end-state fields); `Reporter` interface; `Client` (HTTP POST to `/outcome`, non-fatal, configurable timeout); no-op when `Addr` is empty.
- **`AuditConfig`** in `internal/config` — `Enabled` (default true), `FilePath` (default `./data/audit.jsonl`); env vars `AUDIT_ENABLED`, `AUDIT_FILE_PATH`.
- **`LearnerConfig`** in `internal/config` — `Addr` (default `""` = disabled), `Timeout` (5s); env vars `LEARNER_ADDR`, `LEARNER_TIMEOUT`.
- **Pipeline audit trail** — `runPipeline` now generates a `TraceID` (`uid.New()`) at start; emits an `AuditEvent` at every stage (Detected, Diagnosed, Decided, ApprovalRequested, ApprovalResolved, DryRun, Applied, Verified, Notified, Escalated). Sink errors are logged and swallowed — a failing sink never breaks the pipeline.
- **Outcome reporting** — `runPipeline` defers an `outcome.Record` post after policy-decision stage is reached; applied/verification fields populated as pipeline progresses; reporter errors are non-fatal.
- **`autosre audit` CLI** — `autosre audit [--incident] [--trace] [--since] [--stage] [--limit] [--json]`; queries the JSONL file and prints human-readable or raw JSON.
- **Learner FastAPI service** — `POST /outcome` (append-only, 204), `GET /stats` (read-only advisory success rates by `failure_mode/action`), `GET /healthz`; backed by JSONL `OutcomeStore` + pure `compute_stats` aggregator.
- **Python tests** — `test_store.py` (7 tests), `test_aggregator.py` (7 tests), `test_server.py` (10 tests); all use `tmp_path`/`monkeypatch`; no network or cluster calls.

### Safety Invariants
- **Non-fatal audit**: sink error = log + continue; pipeline never aborted by audit.
- **Advisory-only learning**: stats never read by policy engine, diagnosis, or any control-flow path. `GET /stats` has no side effects.
- **Append-only**: `AuditSink` interface has no `Update`/`Delete`; `OutcomeStore` has no `update`/`delete`/`clear` methods.
- **No DB**: JSONL for events, JSONL for outcomes. Durable store marked with `// TODO`.
- **No cluster writes**: audit and outcome packages import only stdlib + uid. Safety grep CLEAN.

### Files Created
- `agent/internal/audit/audit.go` — types, interface, QueryFilter, NoOp
- `agent/internal/audit/memory_sink.go` — in-memory goroutine-safe sink
- `agent/internal/audit/file_sink.go` — append-only JSONL file sink
- `agent/internal/audit/audit_test.go` — 10 tests
- `agent/internal/outcome/outcome.go` — Record, Reporter interface
- `agent/internal/outcome/client.go` — HTTP client
- `agent/cmd/autosre/audit.go` — `autosre audit` CLI
- `learner/learner/contracts.py` — Outcome, FailureModeActionStats, StatsResponse
- `learner/learner/store.py` — OutcomeStore (append-only JSONL)
- `learner/learner/aggregator.py` — compute_stats (pure function)
- `learner/learner/server.py` — FastAPI service
- `learner/tests/test_store.py`, `test_aggregator.py`, `test_server.py`

### Files Modified
- `agent/internal/config/config.go` — added `AuditConfig`, `LearnerConfig`; wired into `Config`/`Load()`
- `agent/internal/orchestrator/orchestrator.go` — added `sink`/`outcomes` fields; updated `New()` (2 new params); added `record()`/`reportOutcome()` non-fatal helpers
- `agent/internal/orchestrator/pipeline.go` — audit events at every stage; outcome record collection + deferred post
- `agent/internal/orchestrator/orchestrator_test.go` — updated `newTestOrchestrator` (nil, nil for new params); 2 new audit tests
- `agent/cmd/autosre/run.go` — wire `audit.FileSink` + `outcome.Client`; log on disable/error
- `agent/cmd/autosre/main.go` — added `"audit"` subcommand dispatch
- `learner/learner/main.py` — replaced placeholder with real uvicorn entrypoint
- `learner/pyproject.toml` — added fastapi, uvicorn, pydantic, httpx deps; fixed build-backend
- `.env.example` — added audit + learner env vars

---

## Prompt 7 — Orchestrator (2026-06-16)

### Features Added
- **`OrchestratorConfig`** in `internal/config` — `ApplyEnabled` (default `false`), `KillSwitch`, `MaxWorkers` (5), `DefaultContainer` ("app"), `DefaultScaleReplicas` (2), `PolicyFile`; all backed by env vars.
- **`DiagnosisClient` interface** in `internal/orchestrator` — decouples orchestrator from `*diagnosis.Client` for testing.
- **`Verifiable` interface** in `internal/orchestrator` — decouples orchestrator from `*verifier.Verifier` for testing.
- **`ActionBuilder` interface** in `internal/orchestrator` — decouples orchestrator from concrete remediator actions for testing.
- **`defaultActionBuilder`** — production `ActionBuilder` backed by real remediator actions (`BumpMemoryLimit`, `RollbackDeployment`, `ScaleDeployment`); falls back to configured defaults when Params are zero/missing.
- **`Orchestrator`** — wires detect → diagnose → decide → (dry-run) remediate → verify → notify into a bounded, idempotent, graceful-shutdown-aware reconcile loop.
- **`inFlightRegistry`** — per-incident idempotency guard; `tryAcquire/release` prevent the same incident ID from being processed concurrently.
- **Kill switch** (`atomic.Bool`) — halts all Apply calls immediately, regardless of verdict; checked before every write; configurable via `ORCHESTRATOR_KILL_SWITCH` env var or `SetKillSwitch()` at runtime.
- **Bounded concurrency** — buffered semaphore (`chan struct{}`) caps parallel pipeline goroutines at `MaxWorkers`.
- **7-stage pipeline** (`runPipeline`): diagnose → propose → policy-decide → kill-check → build-action → DryRun → Apply(gated) → Verify → Notify.
- **`Run(ctx, events)`** — consumes `EventClosed` from the correlator; launches goroutines via `schedule()`; stops on ctx cancel or closed channel.
- **`SetKillSwitch` / `KillSwitchEngaged` / `InFlightCount`** — runtime observability.
- **`autosre run` CLI** — starts the full agent: HTTP server (webhook + incidents + health + `/slack/interactions`) + correlator + orchestrator loop; `--apply` flag enables GitOps commits (dry-run-only without it).

### Safety Invariants
- **DRY-RUN-ONLY BY DEFAULT**: `Apply()` is never called unless `cfg.ApplyEnabled = true` (or `--apply` passed). Default env is `ORCHESTRATOR_APPLY_ENABLED=false`.
- **Kill switch**: `atomic.Bool` checked before every `Apply()`. Engaging it mid-run prevents all subsequent writes without stopping the observability loop.
- **Approval fails closed**: `REQUIRE_APPROVAL` verdict without explicit `APPROVED` → no action taken (inherits `MockNotifier` / `SlackNotifier` fail-closed guarantee from Prompt 6).
- **No policy bypass**: all 6 policy gates run before any action is built or called; `BLOCK` → Notify + return; no short-circuit.
- **No new cluster writes**: orchestrator only calls existing `remediator` actions via the `contracts.RemediationAction` interface. Safety grep CLEAN.
- **No change to underlying safety semantics** of policy, remediator, verifier, or notifier.

### Files Created
- `agent/internal/orchestrator/orchestrator.go` — `Orchestrator`, `New()`, `Run()`, `schedule()`, `SetKillSwitch()`, `InFlightCount()`, `inFlightRegistry`, `DiagnosisClient`/`Verifiable` interfaces
- `agent/internal/orchestrator/pipeline.go` — 7-stage `runPipeline()`, `buildProposal()`
- `agent/internal/orchestrator/builder.go` — `ActionBuilder` interface, `defaultActionBuilder`, `NewDefaultBuilder()`
- `agent/internal/orchestrator/orchestrator_test.go` — 15 tests (pipeline stages, idempotency, kill switch, Run loop)
- `agent/cmd/autosre/run.go` — `autosre run` CLI subcommand

### Files Modified
- `agent/internal/config/config.go` — added `OrchestratorConfig`; wired into `Config` and `Load()`
- `agent/cmd/autosre/main.go` — added `"run"` to subcommand dispatch
- `.env.example` — added 5 orchestrator env vars
- `docs/PROJECT_PROGRESS.md` — updated phase, completed prompts, modules, env vars, folder structure
- `docs/CHANGELOG.md` — this entry

### Notes
- 15 new Go tests in `internal/orchestrator/`. Total platform Go tests: ~125.
- No new Go module dependencies.
- Tests use `MockNotifier` (from Prompt 6) and in-package test doubles for `DiagnosisClient`, `ActionBuilder`, `Verifiable` — no network or cluster calls.
- The duplicate-incident test (`TestOrchestrator_DuplicateIncident`) uses a blocking `mockAction.DryRun` to hold the in-flight lock, then verifies the second call is a no-op.
- `autosre run --apply` is the first end-to-end path; without `--apply` it performs the full observe-diagnose-decide-verify-notify cycle in safe read-only mode.

---

## Prompt 6 — Notifier (2026-06-16)

### Features Added
- **`ApprovalDecision`** enum in `contracts` — `APPROVED`, `DENIED`, `TIMEOUT` (TIMEOUT treated as DENIED downstream — fail-closed).
- **`ApprovalResult`** struct in `contracts` — `RequestID`, `Decision`, `Approver`, `DecidedAt`, `Reason`.
- **`Notifier` interface** refined — `RequestApproval` now takes `RemediationProposal` (not `RemediationAction`) and returns `ApprovalResult`. `Notify`/`Escalate` unchanged.
- **`SlackNotifier`** — `Notify()` posts text to Slack channel; `RequestApproval()` posts Block Kit buttons, blocks until approve/deny/timeout, fail-closed; `Escalate()` posts alert; `InteractionsHandler()` returns HTTP handler for `POST /slack/interactions`.
- **Slack signature verification** — HMAC-SHA256 with signing secret + timestamp; replays >5 min old rejected (401); empty signing secret rejects all inbound.
- **In-memory approval registry** — thread-safe `map[string]*pendingEntry`; keyed by request ID; auto-cleaned on resolution or cancellation.
- **`PagerDutyClient`** — Posts to Events API v2 (`trigger`) with dedup key, severity, custom details. Degrades to log-only when routing key absent or transport fails.
- **`CompositeNotifier`** — Routes `Notify`/`RequestApproval` to Slack; `Escalate` to both Slack and PagerDuty; `New(cfg)` constructor.
- **`MockNotifier`** — Records all calls; configurable `ApprovalResult`; default DENIED (fail-closed); satisfies `contracts.Notifier`; no network calls.
- **`NotifierConfig`** — 7 env vars: `SLACK_BOT_TOKEN`, `SLACK_SIGNING_SECRET`, `SLACK_CHANNEL_ID`, `PAGERDUTY_ROUTING_KEY`, `NOTIFIER_APPROVAL_TIMEOUT`, `NOTIFIER_SEND_TIMEOUT`, `NOTIFIER_MAX_RETRIES`.
- **`autosre notify` CLI** — `--type summary|escalate` from incident file; dry-run by default (prints payload); `--send` flag + credentials for real sends; never remediates.

### Safety Invariants
- Notifier never calls remediator, gitwriter, or any k8s API. Safety grep CLEAN.
- `RequestApproval` fails closed: timeout / missing channel / post failure / context cancel → `DENIED`.
- Transport outages degrade to log-only; never panic or block the caller.
- Inbound `/slack/interactions` requires valid HMAC-SHA256 Slack signature; empty signing secret = reject all.

### Files Created
- `agent/internal/notifier/doc.go` — package doc + compile-time interface assertions
- `agent/internal/notifier/registry.go` — in-memory pending approval registry
- `agent/internal/notifier/slack.go` — SlackNotifier + signature verification + Block Kit messages
- `agent/internal/notifier/pagerduty.go` — PagerDutyClient (Events API v2)
- `agent/internal/notifier/composite.go` — CompositeNotifier + New() constructor
- `agent/internal/notifier/mock.go` — MockNotifier for tests
- `agent/internal/notifier/notifier_test.go` — 16 tests (all transport-mocked, no real sends)
- `agent/cmd/autosre/notify.go` — `autosre notify` CLI

### Files Modified
- `agent/internal/contracts/contracts.go` — added `ApprovalDecision`, `ApprovalResult`; refined `Notifier` interface
- `agent/internal/config/config.go` — added `NotifierConfig`; wired into `Config` and `Load()`
- `agent/cmd/autosre/main.go` — added `"notify"` to subcommand dispatch
- `agent/internal/notifier/.gitkeep` — removed (replaced by real files)
- `.env.example` — added 7 notifier env vars; replaced placeholder comment block
- `docs/PROJECT_PROGRESS.md` — updated phase, modules, env vars
- `docs/CHANGELOG.md` — this entry

### Notes
- 44 Python tests still pass (unchanged).
- 16 new Go tests in `internal/notifier/` bring total platform Go tests to ~110.
- No new Go module dependencies — all HTTP transport via `net/http` std library.
- `MockNotifier` is in production code (not `_test.go`) so the orchestrator (Prompt 7) can use it in its own tests.

---

## Prompt 5 — Verifier (2026-06-16)

### Features Added
- **`VerificationOutcome`** enum in `contracts` — `RECOVERED`, `FAILED`, `INCONCLUSIVE`.
- **`VerificationResult`** struct in `contracts` — `IncidentID`, `RemediationRef`, `Outcome`, `EscalationNeeded`, `ObservedSignals`, `WindowStart`, `WindowEnd`, `Reason`. Fully populated on all outcome paths.
- **`RecoverySource` interface** — `RecentSignalsFor(target, since) []Signal` and `IsIncidentActive(id) bool`; decouples verifier from correlator for testing.
- **`CorrelatorSource`** — production `RecoverySource` backed by `*correlator.Correlator`; calls `ListIncidents()` (read-only).
- **`Verifier.Verify(ctx, incident, remediationRef)`** — two-phase: grace delay (ArgoCD sync time) then polling observation window; FAIL TOWARD ESCALATION bias; `RECOVERED` only on sustained clean window; any context cancel/ambiguity → `INCONCLUSIVE` with `EscalationNeeded=true`.
- **`VerifierConfig`** — `GraceDelay` (default 30s), `Window` (5m), `PollInterval` (15s), `FailureThreshold` (0) — all configurable via env vars.
- **`autosre verify` CLI** — loads incident from file or fixture, runs verifier against `cliRecoverySource` (static read of fixture signals), prints `VerificationResult`; executes nothing.

### Safety Invariant
Verifier is read-only. No cluster writes, no `client-go` mutations, no remediator/gitwriter calls, no notifier calls. Safety grep (excluding comments) returns CLEAN. `EscalationNeeded` is always `true` on FAILED/INCONCLUSIVE; always `false` on RECOVERED.

### Files Created
- `agent/internal/verifier/source.go` — `RecoverySource` interface + `CorrelatorSource`
- `agent/internal/verifier/verifier.go` — `Verifier`, grace-delay + polling loop, `mergeSignals`, helpers
- `agent/internal/verifier/verifier_test.go` — 11 tests (functional + edge + boundary + contract types)
- `agent/cmd/autosre/verify.go` — `autosre verify` CLI + `cliRecoverySource`

### Files Modified
- `agent/internal/contracts/contracts.go` — added `VerificationOutcome`, `VerificationResult`
- `agent/internal/config/config.go` — added `VerifierConfig`, `getInt()` helper; wired into `Config` and `Load()`
- `agent/cmd/autosre/main.go` — added `"verify"` to subcommand dispatch
- `agent/internal/verifier/.gitkeep` — removed (replaced by real files)
- `.env.example` — added `VERIFIER_GRACE_DELAY`, `VERIFIER_WINDOW`, `VERIFIER_POLL_INTERVAL`, `VERIFIER_FAILURE_THRESHOLD`
- `docs/PROJECT_PROGRESS.md` — updated phase, modules, env vars
- `docs/CHANGELOG.md` — this entry

### Notes
- 44 Python tests still pass (unchanged).
- 11 new Go tests in `internal/verifier/`. Total platform Go tests: ~94.
- No new Go module dependencies.
- `futureSig` test helper sets `ReceivedAt = now+1h` so pre-populated signals are visible inside the observation window (window opens after `Verify()` starts).

---

## Prompt 4 — LLM Diagnosis Layer (2026-06-16)

### Features Added
- **`GeminiProvider`** — calls Gemini API via `google-generativeai`; enforces action whitelist and confidence clamping; explicit prompt-injection defense (telemetry in `<TELEMETRY_DATA>` delimiters; system prompt marks it untrusted); falls back on any error (timeout, network, bad JSON, disallowed action).
- **`RuleBasedProvider`** — deterministic failure-mode→action mapping (OOMKilled→bump-memory-limit, CrashLoopBackOff→rollback-deployment, etc.); no network calls; always available.
- **`DiagnosisService`** — wraps providers with fallback; runs fallback-only when `GEMINI_API_KEY` is absent (no crash).
- **FastAPI HTTP bridge** — `POST /diagnose` accepts Incident JSON, returns Diagnosis JSON; `GET /healthz`; Pydantic validation (400 on malformed); exception handler (500 on unexpected errors); never executes remediation.
- **Go `diagnosis.Client`** — posts Incident (snake_case JSON) to diagnoser; defensive confidence clamping; returns error when service unreachable (no silent fallback in Go).
- **`autosre diagnose` CLI** — posts a sample or file-supplied incident, prints Diagnosis; executes nothing.
- **`Diagnosis.Source`** — new field on `contracts.Diagnosis` (Go + Python): `"gemini"` or `"fallback"`.
- **JSON struct tags** added to `contracts.Diagnosis` in Go for correct snake_case serialization.
- **Sample incident fixture** at `agent/internal/diagnosis/testdata/sample_incident.json`.

### Safety Invariant
Diagnoser is advisory-only. It never calls the remediator, gitwriter, policy engine, or any Kubernetes API. Grep (excluding comments) returns CLEAN. `proposed_action` is constrained to the whitelist `{rollback-deployment, scale-deployment, bump-memory-limit}` in both Python (rejection → fallback) and Go (confidence clamped defensively).

### Files Created
- `diagnoser/diagnoser/providers/__init__.py`
- `diagnoser/diagnoser/providers/rule_based.py` — deterministic fallback
- `diagnoser/diagnoser/providers/gemini.py` — GeminiProvider + `_parse_and_validate`
- `diagnoser/diagnoser/core.py` — DiagnosisService
- `diagnoser/diagnoser/server.py` — FastAPI bridge
- `diagnoser/tests/test_providers.py` — 18 tests (RuleBasedProvider + mocked GeminiProvider)
- `diagnoser/tests/test_core.py` — 8 tests (DiagnosisService fallback logic)
- `diagnoser/tests/test_server.py` — 9 tests (HTTP bridge)
- `agent/internal/diagnosis/client.go` — Go HTTP client + DTOs
- `agent/internal/diagnosis/client_test.go` — 8 tests (httptest server)
- `agent/internal/diagnosis/testdata/sample_incident.json` — fixture
- `agent/cmd/autosre/diagnose.go` — `autosre diagnose` CLI

### Files Modified
- `diagnoser/diagnoser/contracts.py` — added `kind`, `reason`, `message` to Signal; `updated_at` to Incident; `source` to Diagnosis
- `diagnoser/diagnoser/main.py` — full uvicorn entrypoint replacing placeholder
- `diagnoser/pyproject.toml` — added fastapi, uvicorn, pydantic, google-generativeai, httpx; fixed build-backend to `setuptools.build_meta`
- `agent/internal/contracts/contracts.go` — added `Source` field + JSON struct tags to Diagnosis
- `agent/internal/config/config.go` — added `DiagnoserConfig` (Addr, Timeout)
- `agent/cmd/autosre/main.go` — added `"diagnose"` to subcommand dispatch
- `.env.example` — added Gemini + diagnoser env vars
- `docs/PROJECT_PROGRESS.md` — updated phase, modules, env vars
- `docs/CHANGELOG.md` — this entry

### Notes
- 44 Python tests pass (35 new: 18 providers + 8 core + 9 server; 9 existing from P0).
- 8 new Go tests in `internal/diagnosis/` bring total to ~83 across the platform.
- No new Go module dependencies; google-generativeai is a Python dep only.
- `utcnow()` deprecation warnings in tests are cosmetic (Python 3.12 prefers tz-aware datetimes); not affecting functionality. Will be addressed in a future cleanup prompt.
- TODO markers left at: orchestrator (wiring diagnose into control loop), verifier, notifier.

---

## Prompt 3 — Decision / Policy Engine (2026-06-16)

### Features Added
- **New contract types** — `Verdict` (AUTO/REQUIRE_APPROVAL/BLOCK), `AutonomyLevel` (observe/propose/auto-with-approval/full-auto), `Decision` struct (Verdict, Reason, MatchedRules, DryRunRequired), `RemediationProposal` struct (IncidentID, Namespace, Resource, FailureMode, Confidence, Params), `ActionParams` struct.
- **Policy engine** (`internal/policy.Engine`) — pure, deterministic `Evaluate(proposal) Decision` applying 6 ordered gates, each able only to downgrade (never upgrade) the verdict:
  1. Confidence validation (out-of-range → BLOCK)
  2. Autonomy level resolution (global default → failure-mode override → namespace override; most-restrictive-wins)
  3. Action allow-list per failure mode (unlisted or unknown mode → BLOCK)
  4. Blast-radius limits (replica delta, memory bump factor)
  5. Protected namespaces (→ REQUIRE_APPROVAL)
  6. Circuit breaker (in-memory rolling window; trips → REQUIRE_APPROVAL)
  7. Dry-run annotation (sets DryRunRequired on AUTO decisions per policy)
- **Policy-as-code config** (`PolicyConfig`, `LoadPolicyFile`) — YAML loader using yaml.v3; fail-closed on missing/invalid file (returns conservative defaults, never crashes, never AUTO).
- **Sample `policy.yaml`** — conservative defaults (defaultAutonomy=propose, threshold=0.90, requireDryRun=true, per-failure-mode allow-lists, protected namespaces, circuit breaker).
- **Circuit breaker** — thread-safe rolling window of AUTO decision timestamps; trips when count ≥ maxActionsPerWindow; self-clears when window expires.
- **`autosre policy` CLI** — evaluates a proposal from flags, prints Decision (verdict + reason + matched rules); executes no remediation. Supports `--json` output.

### Safety Invariant
`internal/policy/` contains zero calls to remediator, gitwriter, Kubernetes API, or exec. Grep-verified CLEAN.
Confidence is an input — not computed here. TODO markers left for diagnoser (Prompt 4) and orchestrator seams.

### Files Created
- `agent/internal/policy/config.go` — `PolicyConfig`, `LoadPolicyFile`, `defaultPolicy`, validation
- `agent/internal/policy/circuitbreaker.go` — thread-safe rolling-window circuit breaker
- `agent/internal/policy/engine.go` — `Engine`, `Evaluate()`, all 6 gate implementations
- `agent/internal/policy/engine_test.go` — 20 unit/integration tests
- `agent/cmd/autosre/policy.go` — `runPolicy()` CLI subcommand
- `policy.yaml` — sample policy-as-code config

### Files Modified
- `agent/internal/contracts/contracts.go` — added Verdict, AutonomyLevel, Decision, ActionParams, RemediationProposal (backward-compatible)
- `agent/cmd/autosre/main.go` — added `policy` to subcommand dispatch switch
- `.env.example` — added POLICY_FILE
- `docs/PROJECT_PROGRESS.md` — updated phase, modules, env vars, folder structure, architectural decisions
- `docs/CHANGELOG.md` — this entry

### Notes
- 20 new tests: functional (happy paths), edge/boundary (out-of-range confidence, unknown failure mode, exact threshold), circuit-breaker (trip + clear), missing policy file.
- No new Go module dependencies added — `gopkg.in/yaml.v3` already in go.mod from Prompt 2.
- TODO (future prompt) markers at: diagnoser confidence supply, orchestrator wiring, notifier REQUIRE_APPROVAL routing.
- Total tests: 34 (P1) + 21 (P2) + 20 (P3) = 75.

---

## Prompt 2 — GitOps Remediation Primitives (2026-06-16)

### Features Added
- **`gitwriter` package** — shared YAML edit + git commit engine using `gopkg.in/yaml.v3` Node API (structure-preserving) and `go-git` (no shell exec). Provides `EditField`, `GetCurrentValue`, `GetPreviousValue`.
- **`BumpQuantity`** — multiplies a Kubernetes memory quantity string (e.g. `"256Mi"`) by a factor, preserving suffix, ceiling-rounding the result.
- **`FindManifest`** — walks a gitops repo to find a YAML file matching `(kind, namespace, name)`.
- **`GetField` / `SetField`** — navigate and edit dot-separated field paths including array selectors (`containers[name=app]`).
- **`RollbackDeployment`** — reverts a container image to a known-good tag (or discovers it from git history).
- **`ScaleDeployment`** — changes `spec.replicas` to a target count.
- **`BumpMemoryLimit`** — increases `resources.limits.memory` by a configurable factor (default 1.5×).
- All three actions implement `contracts.RemediationAction`; compile-time checks in `remediator/doc.go`.
- **`autosre remediate` subcommand** — manual CLI trigger with `--action`, `--namespace`, `--deployment`, `--container`, `--replicas`, `--known-good`, `--factor`, `--apply` (default: dry-run), `--repo`.
- **`gitops/apps/production/payment-service.yaml`** — fixture Deployment for local testing.

### Safety Invariant
Every remediation is a git commit only. No `client-go` create/update/patch/delete/scale calls exist in `remediator/` or `gitwriter/`. Verified by grep (returns clean).

### Files Created
- `agent/internal/gitwriter/quantity.go` — `BumpQuantity()`, `ParseQuantityBytes()`
- `agent/internal/gitwriter/manifest.go` — `FindManifest()`, `GetField()`, `SetField()`, YAML navigation helpers
- `agent/internal/gitwriter/gitwriter.go` — `Writer`, `Config`, `Result`, `EditField()`, `GetCurrentValue()`, `GetPreviousValue()`
- `agent/internal/gitwriter/gitwriter_test.go` — 12 integration tests (temp git repo)
- `agent/internal/remediator/doc.go` — package doc + compile-time interface checks
- `agent/internal/remediator/scale.go` — `ScaleDeployment` action
- `agent/internal/remediator/rollback.go` — `RollbackDeployment` action
- `agent/internal/remediator/bump_memory.go` — `BumpMemoryLimit` action
- `agent/internal/remediator/remediator_test.go` — 9 integration tests
- `agent/cmd/autosre/remediate.go` — `autosre remediate` CLI subcommand
- `gitops/apps/production/payment-service.yaml` — fixture Deployment

### Files Modified
- `agent/go.mod` — added `github.com/go-git/go-git/v5 v5.11.0`, `gopkg.in/yaml.v3 v3.0.1` + go-git indirect deps
- `agent/internal/config/config.go` — added `RemediatorConfig` (RepoPath, BotName, BotEmail, Branch, DefaultDryRun, MemoryBumpFactor); added `strconv` import + `getFloat()` helper
- `agent/cmd/autosre/main.go` — added subcommand dispatch (`autosre remediate` → `runRemediate`); updated banner
- `.env.example` — added GitOps remediation env vars (GITOPS_REPO_PATH, GIT_BOT_NAME, etc.)
- `docs/PROJECT_PROGRESS.md` — updated phase, module list, folder structure, env vars, architectural decisions
- `docs/CHANGELOG.md` — this entry

### Notes
- Dry-run is the default for all three actions; `--apply` is required to commit. This matches `REMEDIATION_DRY_RUN=true` default.
- `GetPreviousValue` walks `git log --path-filter` to find the second-most-recent commit touching the manifest; used by `RollbackDeployment` when `--known-good` is not supplied.
- NoOp is detected pre-commit (old == new) to avoid empty commits and spurious audit noise.
- File is atomically restored to original content if the git commit fails for any reason.
- 21 new tests (12 gitwriter + 9 remediator) in addition to the 34 from Prompt 1 = 55 total.

---

## Prompt 1 — Detection: Signal Ingestion + Correlation (2026-06-16)

### Features Added
- **Kubernetes event watcher** — SharedInformerFactory watches v1.Event (Warning type) for OOMKilling, BackOff, FailedScheduling, NodeNotReady, Failed+ImagePullBackOff, Killing+OOM; v1.Pod for CrashLoopBackOff/ImagePullBackOff/OOMKilled container states; v1.Node for NotReady conditions.
- **Alertmanager webhook receiver** — `POST /webhook/alertmanager` parses v4 payload; validates JSON; emits one Signal per alert; returns 400 on malformed input.
- **Signal deduplication** — identical (resource, reason) signals within `DEDUP_WINDOW` (default 1m) are dropped.
- **Incident correlation** — signals for the same `namespace/resource` within `CORRELATION_WINDOW` (default 5m) are grouped into one Incident.
- **Incident auto-close** — background resolver closes open Incidents with no new signals for `RESOLVE_WINDOW` (default 10m).
- **Incident inspection endpoint** — `GET /incidents` returns a JSON snapshot of all incidents.
- **Health endpoint** — `GET /health` returns `{"status":"ok"}`.
- **Graceful shutdown** — SIGINT/SIGTERM cancels root context; HTTP server drains with 5s timeout.
- **Structured logging** — `log/slog` JSON handler; every incident lifecycle transition is logged (opened / updated / closed).

### Files Created
- `agent/internal/uid/uid.go` — crypto/rand-backed 16-char hex ID generator
- `agent/internal/config/config.go` — env-var config loader with Validate()
- `agent/internal/ingestor/ingestor.go` — Ingestor struct: Start(), Signals(), WebhookHandler()
- `agent/internal/ingestor/k8s.go` — k8s event/pod/node watcher + mapEvent/mapPodCrash
- `agent/internal/ingestor/k8s_test.go` — 14 unit tests for mapping logic
- `agent/internal/ingestor/webhook.go` — Alertmanager webhook handler + payload types
- `agent/internal/ingestor/webhook_test.go` — 7 unit tests (happy path + error cases)
- `agent/internal/correlator/correlator.go` — Correlator: Process(), Run(), ResolveStale(), ListIncidents(), Events(), IncidentsHandler()
- `agent/internal/correlator/correlator_test.go` — 13 unit tests

### Files Modified
- `agent/internal/contracts/contracts.go` — added `Kind`, `Reason`, `Message` to Signal; added `UpdatedAt` to Incident
- `agent/internal/contracts/contracts_test.go` — updated for new Signal/Incident fields
- `agent/cmd/autosre/main.go` — replaced stub with full detection wiring (ingestor + correlator + HTTP server + graceful shutdown)
- `agent/go.mod` — added `k8s.io/client-go v0.29.3`, `k8s.io/api v0.29.3`, `k8s.io/apimachinery v0.29.3` + known indirect deps
- `.github/workflows/ci.yml` — added `go mod tidy` step before Go lint and test jobs
- `.env.example` — added `IN_CLUSTER`, `WEBHOOK_ADDR`, `CORRELATION_WINDOW`, `RESOLVE_WINDOW`, `DEDUP_WINDOW`
- `docs/PROJECT_PROGRESS.md` — updated with Prompt 1 completion
- `docs/ERROR_LOG.md` — added go.sum missing entry
- `docs/CHANGELOG.md` — this file

### Files Deleted
- `agent/internal/ingestor/.gitkeep` — replaced by real Go files
- `agent/internal/correlator/.gitkeep` — replaced by real Go files

### Notes
- Loki log-tail ingestion is intentionally deferred — `// TODO (future prompt)` marker in `ingestor.go`.
- Correlation key is `namespace/resource`; will be refined to owner references in a future prompt.
- Incident store is in-memory only; lost on restart. Persistence is a future prompt.
- `go.sum` is not committed because Go toolchain is unavailable locally; CI runs `go mod tidy` transiently. Run `go mod tidy` locally once Go 1.22 is installed.
- 34 total unit tests across the detection layer (14 k8s mapping + 7 webhook + 13 correlator).

---

## Prompt 0 — Foundation Setup (2026-06-15)

### Features Added
*(None — scaffolding and interface stubs only, no feature logic)*

### Files Created

**Repository root**
- `.gitignore`
- `.env.example`
- `README.md`
- `Makefile`
- `kind-config.yaml`

**Go agent (`agent/`)**
- `agent/go.mod`
- `agent/.golangci.yml`
- `agent/cmd/autosre/main.go` — startup banner, clean exit
- `agent/internal/contracts/contracts.go` — `Signal`, `Incident`, `Diagnosis`, `RemediationAction`, `LLMProvider`, `Notifier`
- `agent/internal/contracts/contracts_test.go` — table-driven structural tests
- `agent/internal/{ingestor,correlator,policy,remediator,verifier,notifier,audit,controller}/.gitkeep`

**Python diagnoser (`diagnoser/`)**
- `diagnoser/pyproject.toml`
- `diagnoser/diagnoser/__init__.py`
- `diagnoser/diagnoser/contracts.py` — Python mirror of shared types
- `diagnoser/diagnoser/main.py` — placeholder entrypoint
- `diagnoser/tests/__init__.py`
- `diagnoser/tests/test_contracts.py` — structural + parametrized tests

**Python learner (`learner/`)**
- `learner/pyproject.toml`
- `learner/learner/__init__.py`
- `learner/learner/main.py` — placeholder entrypoint
- `learner/tests/__init__.py`
- `learner/tests/test_placeholder.py` — trivial passing test

**Empty skeleton directories (`.gitkeep`)**
- `infra/`, `charts/`, `gitops/`, `web-ui/`, `runbooks/`

**CI**
- `.github/workflows/ci.yml` — lint + test for Go, diagnoser, learner; no deploy

**Docs**
- `docs/CONVENTIONS.md`
- `docs/PROJECT_PROGRESS.md`
- `docs/ERROR_LOG.md`
- `docs/CHANGELOG.md`

### Files Modified
*(None — initial commit)*

### Files Deleted
*(None)*

### Notes
- Established monorepo skeleton with Go agent and two Python services.
- All shared contracts are interface/type stubs only; no implementations.
- Local dev uses `kind` (Kubernetes-in-Docker); no real EKS provisioned.
- CI runs lint + test jobs only; build-and-push and deploy steps deliberately absent.
- Go is not installed on the local dev machine; `go.mod` was created manually.
