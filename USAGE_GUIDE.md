# AutoSRE — Complete Usage Guide

This document tells you exactly what you need, where to put credentials, and how to run the entire platform from scratch.

---

## What Is This Platform?

AutoSRE is a self-healing Kubernetes operations system. When something breaks in your cluster (a pod crashes, memory limit is hit, a deployment goes bad), the platform:

1. **Detects** the failure from Kubernetes events, Alertmanager webhooks, or Loki logs
2. **Diagnoses** the root cause using an AI model (NVIDIA NIM / Gemini) or built-in rules
3. **Decides** what action to take based on your `policy.yaml`
4. **Notifies** your team on Slack and requests human approval (if the policy requires it)
5. **Remediates** by committing a YAML fix to your GitOps repo — ArgoCD then syncs it to the cluster
6. **Verifies** that the fix actually resolved the incident
7. **Learns** from each outcome to improve future decisions

All of this is visible in the Web Dashboard at `http://localhost:8080`.

---

## Quick Start: The Setup Wizard (Recommended)

As of this version, Loki and Alertmanager no longer require editing `.env` or restarting
anything. After running `./start.sh` (see Part 3), open `http://localhost:8080` — on a fresh
install you'll be redirected to **`/setup`**, a short wizard that walks through:

1. **Welcome** — what you're about to configure
2. **Loki** — paste a URL, click **Test connection**, click **Save** (applied live, no restart)
3. **Alertmanager** — copy the generated webhook URL/YAML snippet, or click **Apply
   automatically** if your cluster runs the Prometheus Operator
4. **Done** — you're watching for incidents

You can skip any step and come back later — everything in the wizard is also available
permanently at **`/integrations`**, where you can edit settings, re-test connections, and see
live health (last poll time, last error, whether Alertmanager's Operator CRD was detected).

**Kubernetes is intentionally not part of this wizard.** The agent detects its own cluster
access automatically from `IN_CLUSTER`/`KUBECONFIG` (see Part 1.2) — no credentials are ever
entered through the web UI, since that would mean storing a credential capable of full cluster
access in the database. The Integrations page shows this detection as a read-only status card.

The rest of this guide (Parts 1–2 below) documents the underlying `.env` variables. They still
work and are the right choice for headless/scripted deployments — but for an interactive
first run, the wizard is faster and is now the recommended path.

---

## Services Overview

| Service | Language | Port | Purpose |
|---|---|---|---|
| **Agent** | Go | 8080 | Core brain — detection, decisions, remediation, REST API, Web UI |
| **Diagnoser** | Python | 8001 | AI-powered root cause analysis using LLM or rule engine |
| **Learner** | Python | 8002 | Tracks remediation outcomes for analytics |

---

## Part 1 — What You Must Provide

### 1.1 Required: Software on Your Machine

Install these before anything else.

| Tool | Minimum Version | How to Install |
|---|---|---|
| **Python** | 3.11 or higher | `sudo apt install python3.11` or `brew install python@3.11` |
| **Go** | 1.22 or higher | Download from https://go.dev/dl/ |
| **Node.js + npm** | 18 or higher | `sudo apt install nodejs npm` or `brew install node` |
| **Git** | Any recent | `sudo apt install git` |
| **curl** | Any | Usually pre-installed |

Check what you have:
```bash
python3 --version
go version
node --version
npm --version
```

### 1.2 Required: Kubernetes Access

The agent connects to Kubernetes to read events and apply fixes. You need:

- A running Kubernetes cluster (local: **kind** / **minikube** / Docker Desktop, OR cloud: EKS / GKE / AKS)
- A valid `~/.kube/config` file that points to your cluster

Check it works:
```bash
kubectl get nodes
```

To create a local cluster with kind (fastest way to try the platform):
```bash
# Install kind
curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.22.0/kind-linux-amd64 && chmod +x kind && sudo mv kind /usr/local/bin/

# Create cluster using the project's config
kind create cluster --config kind-config.yaml
```

### 1.3 Required: GitOps Repository

When AutoSRE fixes something, it commits a YAML change to a git repository. ArgoCD then reads that commit and applies it to the cluster. You need a git repo for this.

**Option A — Use the bundled local gitops folder (for testing)**
The repo already has a `gitops/` folder. Point to it in `.env`:
```
GITOPS_REPO_PATH=/full/path/to/autosre/gitops
```
No push, no ArgoCD. Actions stay local — safe for testing.

**Option B — Use a real GitHub repo (for production)**
1. Create a new GitHub repo (e.g. `your-org/gitops-config`)
2. Clone it somewhere on your machine
3. Set in `.env`:
```
GITOPS_REPO_PATH=/path/to/cloned/gitops-config
GIT_REMOTE_URL=https://github.com/your-org/gitops-config.git
GIT_TOKEN=ghp_yourGitHubPersonalAccessToken
```

How to get a GitHub token:
- Go to https://github.com/settings/tokens
- Click "Generate new token (classic)"
- Select scope: `repo` (full control of private repositories)
- Copy the token — it starts with `ghp_`

### 1.4 Optional but Recommended: NVIDIA NIM API Key

This gives the platform real AI-powered diagnosis. Without it, it falls back to simple rule-based logic.

- Go to: https://build.nvidia.com
- Sign up / log in
- Find model `meta/llama-3.3-70b-instruct`
- Click "Get API Key"
- Copy the key — it starts with `nvapi-`

Set in `.env`:
```
NIM_API_KEY=nvapi-yourKeyHere
```

### 1.5 Optional: Slack (for notifications and approvals)

When the platform detects an incident or needs human approval, it messages Slack.

**Create a Slack App:**
1. Go to https://api.slack.com/apps → "Create New App" → "From Scratch"
2. Name it `AutoSRE`, pick your workspace
3. In "OAuth & Permissions" → add Bot Token Scopes:
   - `chat:write`
   - `chat:write.public`
4. Click "Install App to Workspace" → copy the `Bot User OAuth Token` (starts with `xoxb-`)
5. In "Basic Information" → copy the "Signing Secret"
6. Invite the bot to your channel: `/invite @AutoSRE`
7. Get your channel ID: right-click the channel in Slack → "Copy link" → the ID is the last part (starts with `C`)

Set in `.env`:
```
SLACK_BOT_TOKEN=xoxb-your-bot-token
SLACK_SIGNING_SECRET=your-signing-secret
SLACK_CHANNEL_ID=C01234ABCDE
```

### 1.6 Optional: PagerDuty (for escalations)

If Slack approval times out or a critical alert needs immediate escalation:

1. Go to your PagerDuty account → Services → your service → Integrations
2. Add "Events API v2" integration
3. Copy the "Integration Key"

Set in `.env`:
```
PAGERDUTY_ROUTING_KEY=your-32-char-routing-key
```

### 1.7 Optional: Loki (for log-based detection)

If you have Grafana Loki running, the platform can poll it for error logs to detect incidents from application logs directly (in addition to Kubernetes events).

**Recommended:** configure this through the Setup Wizard or the Integrations page
(`/integrations`) in the web UI — no `.env` edit or restart needed, and you get a live "Test
connection" check before saving. See **Quick Start** above.

**Alternative (headless/scripted deployments):** set in `.env`:
```
LOKI_ADDR=http://localhost:3100
```
Note: a setting saved through the web UI takes precedence over this env var on the next
restart (settings store > env var > built-in default).

---

## Part 2 — Setting Up Your .env File

All credentials and configuration live in a single `.env` file. **This file is gitignored — never commit it.**

### Step 1 — Copy the example file

```bash
cd /path/to/autosre
cp .env.example .env
```

### Step 2 — Open and fill in `.env`

Below is a minimal `.env` to get running locally. Lines marked `# REQUIRED` must be filled in.

```bash
# ── Kubernetes ─────────────────────────────────────────
KUBECONFIG=~/.kube/config           # path to your kube config
IN_CLUSTER=false                    # set true only when running inside a pod

# ── GitOps repo ─────────────────────────────────────────
GITOPS_REPO_PATH=/full/path/to/autosre/gitops   # REQUIRED

# For real GitHub push (optional for local testing):
# GIT_REMOTE_URL=https://github.com/your-org/gitops.git
# GIT_TOKEN=ghp_yourtoken

# ── AI Diagnoser (pick one or leave blank for rule-based) ──
NIM_API_KEY=nvapi-yourkey           # NVIDIA NIM (recommended)
NIM_MODEL=meta/llama-3.3-70b-instruct

# ── Slack notifications (optional) ──────────────────────
# SLACK_BOT_TOKEN=xoxb-your-token
# SLACK_SIGNING_SECRET=your-secret
# SLACK_CHANNEL_ID=C01234ABCDE

# ── Safety gates ────────────────────────────────────────
DRY_RUN=true                        # keep true until you're ready for real changes
ORCHESTRATOR_APPLY_ENABLED=false    # set true to allow GitOps commits
POLICY_FILE=./policy.yaml           # which policy rules to use
```

### Where credentials are stored and used

| Credential | Variable | Used by | Never stored in |
|---|---|---|---|
| NVIDIA NIM key | `NIM_API_KEY` | Diagnoser (Python) | Source code, logs |
| Gemini key | `GEMINI_API_KEY` | Diagnoser (Python) | Source code, logs |
| GitHub token | `GIT_TOKEN` | Agent (Go) — git push | Source code, logs |
| Slack bot token | `SLACK_BOT_TOKEN` | Agent (Go) — notifier | Source code, logs |
| Slack signing secret | `SLACK_SIGNING_SECRET` | Agent (Go) — webhook verify | Source code, logs |
| PagerDuty key | `PAGERDUTY_ROUTING_KEY` | Agent (Go) — escalations | Source code, logs |

All of these are read from environment variables at startup — never hardcoded anywhere.

---

## Part 3 — Starting the Platform

### First-time startup (everything automatic)

```bash
cd /path/to/autosre
chmod +x start.sh
./start.sh
```

What `start.sh` does automatically on first run:
1. Loads your `.env` file
2. Creates Python virtual environments for the diagnoser and learner (`diagnoser/.venv`, `learner/.venv`)
3. Installs all Python dependencies inside those venvs
4. Builds the Go agent binary (`agent/autosre`)
5. Builds the React web UI (`web-ui/dist/`)
6. Starts all three services in the background
7. Waits for each to be healthy before moving to the next

On subsequent runs it skips reinstallation if nothing changed (fast startup).

### After startup — what you will see

```
╔══════════════════════════════════════════════════════════════╗
║  AutoSRE is running!                                         ║
╠══════════════════════════════════════════════════════════════╣
║  Web Dashboard  →  http://localhost:8080                     ║
║  API Health     →  http://localhost:8080/api/v1/health       ║
║  Incidents      →  http://localhost:8080/api/v1/incidents    ║
║  Metrics        →  http://localhost:8080/metrics             ║
╚══════════════════════════════════════════════════════════════╝
```

### Other start.sh commands

```bash
./start.sh --status    # show which services are running
./start.sh --logs      # tail live logs from all services
./start.sh --stop      # gracefully stop all services
./start.sh --rebuild   # force-reinstall deps + rebuild binaries
```

---

## Part 4 — The Web Dashboard

Open your browser to: **http://localhost:8080**

### Pages in the Dashboard

#### Login Page (`/login`)
- **What it is:** Enter your Bearer token here if API auth is enabled.
- **Default mode:** Auth is OFF (`API_OIDC_ENABLED=false`), so all requests are automatically granted viewer access. You do not need to log in for local development.
- **When you need a token:** Only when you set `API_OIDC_ENABLED=true` in production. In that case, get a JWT from your OIDC provider (Google, Auth0, etc.) and paste it here. It is stored in your browser's `localStorage`.
- **Auth roles:**
  - `viewer` — read-only (see incidents, audit log, analytics)
  - `operator` — can approve/reject remediations
  - `admin` — can toggle the kill switch

#### Dashboard (`/` — home page)
- Shows all detected incidents in a table
- Color-coded by severity: Critical (red), High (orange), Medium (yellow), Low (green)
- Filter by severity, status, or search by incident ID / resource name
- Auto-refreshes every 30 seconds
- Click any row to see the full incident trace

#### Approvals (`/approvals`)
- Lists remediations waiting for your human approval
- Each card shows: what the AI diagnosed, what action it wants to take, which resource, confidence score
- Click **Approve** to allow the fix to proceed, **Reject** to block it
- Auto-refreshes every 15 seconds
- You will also get a Slack message with Approve/Reject buttons if Slack is configured

#### Audit Log (`/audit`)
- Every event in the system is logged here: detection, diagnosis, decision, approval, remediation, verification
- Filter by incident ID or pipeline stage
- Paginated (50 events per page)
- Expand any row to see full JSON details of what happened

#### Analytics (`/analytics`)
- MTTR (Mean Time To Resolve) calculated from resolved incidents
- Incidents broken down by severity (bar chart)
- Success vs failure rates for remediations

#### Stats (`/stats`)
- Live outcome statistics from the learner service
- Shows which failure modes get fixed most often, which actions succeed

#### Integrations (`/integrations`)
- Configure Loki and Alertmanager without touching `.env` — see Quick Start above
- Kubernetes card shows read-only connectivity status (in-cluster/kubeconfig, server version)
- Loki card: live form with **Test connection** and **Save** (applied immediately, no restart)
- Alertmanager card: webhook URL + YAML snippet with Copy buttons, **Apply automatically**
  (via Prometheus Operator, when detected) and **Send test webhook**

#### Setup Wizard (`/setup`)
- First-run-only redirect target; same functionality as the Integrations page, presented as a
  guided 4-step flow. Dismissing it (Skip or Finish) sets a flag in `localStorage` so it won't
  redirect you again — visit `/setup` directly any time to re-run it.

#### System Status (`/status`)
- Shows the live state of the orchestrator:
  - Is ApplyEnabled on or off?
  - Is the Kill Switch engaged?
  - How many pipelines are currently in flight?
  - Is the circuit breaker tripped?
- Admins can toggle the Kill Switch from here

---

## Part 5 — How an Incident Flows Through the System

This is the full lifecycle of what happens when a pod crashes:

```
[Kubernetes pod crashes]
         │
         ▼
[Ingestor] — picks up the k8s event
         │
         ▼
[Correlator] — groups related signals into one Incident
         │
         ▼
[Diagnoser (Python)] — sends incident to NVIDIA NIM / Gemini
         │           — AI returns: root cause, proposed action, confidence
         ▼
[Policy Engine] — checks policy.yaml:
         │         - Is confidence >= 0.90?
         │         - Is this failure mode allowed to auto-act?
         │         - Is the namespace protected?
         │         - Is the blast radius within limits?
         │         - Has the circuit breaker tripped?
         │
         ├─ verdict: OBSERVE → log only, no action
         │
         ├─ verdict: PROPOSE → notify Slack, wait for human
         │
         ├─ verdict: REQUIRE_APPROVAL → notify Slack + wait for web/Slack approval
         │
         └─ verdict: AUTO → proceed directly to remediation
                  │
                  ▼
         [Remediator] — modifies Kubernetes YAML
                  │     (bump memory, rollback, scale, patch HPA)
                  │
                  ▼
         [GitWriter] — commits the YAML change to your GitOps repo
                  │     ArgoCD detects the commit and syncs to cluster
                  │
                  ▼
         [Verifier] — waits 30s, then observes for 5 minutes
                  │   checks if the pod is healthy again
                  │
                  ▼
         [Learner] — records the outcome (SUCCESS / FAILED)
                  │
                  ▼
         [Audit Log] — every step above is written to data/audit.jsonl
                      and visible in the Web UI Audit Log page
```

---

## Part 6 — Sending Test Alerts (Without a Real Cluster)

The easiest way to test the webhook path end-to-end is the **Send test webhook** button on
`/integrations` — it exercises the real handler and reports success/failure without you needing
to construct a payload by hand.

To do it manually, you can test the full pipeline by POSTing a fake Alertmanager webhook to the agent:

```bash
curl -X POST http://localhost:8080/webhook/alertmanager \
  -H "Content-Type: application/json" \
  -d '{
    "alerts": [{
      "status": "firing",
      "labels": {
        "alertname": "KubePodCrashLooping",
        "namespace": "staging",
        "pod": "my-app-7d9f8b-xkqp2",
        "container": "app",
        "severity": "critical"
      },
      "annotations": {
        "summary": "Pod is crash-looping"
      }
    }]
  }'
```

After sending, open the dashboard at `http://localhost:8080` — you should see a new incident appear within a few seconds.

---

## Part 7 — Policy Configuration (`policy.yaml`)

This file controls how autonomous the system is. **Edit this before enabling real remediation.**

Key settings:

```yaml
# Default: require human approval for everything
defaultAutonomy: propose

# AI must be at least 90% confident to auto-act
confidenceThreshold: 0.90

# Run a dry-run before any real apply
requireDryRun: true
```

Autonomy levels (from least to most autonomous):

| Level | Meaning |
|---|---|
| `observe` | Log only. No Slack message, no action. |
| `propose` | Send Slack notification. No action unless human approves via web/Slack. |
| `auto-with-approval` | Requires Slack/web approval before acting. |
| `full-auto` | Acts immediately without any human approval. |

**Recommended flow for new users:**
1. Start with `defaultAutonomy: propose` (everything requires your approval)
2. Once you trust the AI diagnosis, change specific failure modes to `auto-with-approval`
3. Only set `full-auto` for failure modes you have seen work correctly many times

---

## Part 8 — Enabling Real Remediation (Turning Off Dry-Run)

By default, the platform is in safe mode — it diagnoses and proposes fixes but never actually changes anything in your cluster.

To enable real GitOps commits, change these two settings in `.env`:

```bash
ORCHESTRATOR_APPLY_ENABLED=true   # allow the agent to commit YAML changes
REMEDIATION_DRY_RUN=false         # allow individual actions to run for real
```

Then also set your GitOps repo and credentials:

```bash
GITOPS_REPO_PATH=/path/to/your/gitops-repo
GIT_REMOTE_URL=https://github.com/your-org/gitops-config.git
GIT_TOKEN=ghp_yourGitHubToken
```

After these changes, run `./start.sh --rebuild` to reload configuration.

**Emergency stop — kill switch:**
If something goes wrong, you can halt all remediation immediately without restarting:

```bash
# Via API (if admin token configured)
curl -X POST http://localhost:8080/api/v1/control/kill-switch \
  -H "Content-Type: application/json" \
  -d '{"engaged": true, "reason": "manual halt - investigating"}'

# Or via the System Status page in the Web UI (admin role required)
```

---

## Part 9 — Monitoring the Platform Itself

### Log files

All logs are written to the `logs/` directory:

```bash
cat logs/agent.log       # Go agent: detection, decisions, API requests
cat logs/diagnoser.log   # Python diagnoser: LLM calls, diagnosis results
cat logs/learner.log     # Python learner: outcome recording

./start.sh --logs        # tail all three at once
```

### Prometheus metrics

The agent exposes Prometheus metrics at:
```
http://localhost:8080/metrics
```

Scrape this with your Prometheus instance. Key metrics:
- `autosre_incidents_total` — total incidents detected
- `autosre_remediations_total` — total remediations attempted
- `autosre_pipeline_duration_seconds` — time to complete each pipeline

### Data files

```
data/autosre.db       # SQLite database (incidents, store)
data/audit.jsonl      # append-only audit event log (one JSON per line)
data/outcomes.jsonl   # learner outcome records
```

---

## Part 10 — Full Prerequisites Checklist

Before running `./start.sh` for the first time, confirm everything below:

```
[ ] Python 3.11+ installed  (python3 --version)
[ ] Go 1.22+ installed      (go version)
[ ] Node.js 18+ installed   (node --version)
[ ] npm installed           (npm --version)
[ ] kubectl working         (kubectl get nodes)
[ ] .env file created       (cp .env.example .env)
[ ] GITOPS_REPO_PATH set in .env to a valid directory
[ ] NIM_API_KEY set in .env (or leave blank for rule-based mode)
[ ] start.sh is executable  (chmod +x start.sh)
```

For Slack approvals (optional):
```
[ ] SLACK_BOT_TOKEN set in .env
[ ] SLACK_SIGNING_SECRET set in .env
[ ] SLACK_CHANNEL_ID set in .env
[ ] Bot invited to Slack channel (/invite @AutoSRE)
```

For real remediation (optional — do this after testing):
```
[ ] ORCHESTRATOR_APPLY_ENABLED=true in .env
[ ] REMEDIATION_DRY_RUN=false in .env
[ ] GIT_REMOTE_URL set to your GitOps repo
[ ] GIT_TOKEN set to a GitHub personal access token with repo scope
```

---

## Part 11 — Common Problems and Fixes

### `./start.sh` fails with "Python 3.11+ not found"
Install Python 3.11:
```bash
sudo apt update && sudo apt install python3.11 python3.11-venv
```

### `./start.sh` fails with "Go is not installed"
Install Go:
```bash
# Download and install Go 1.22
curl -OL https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

### Agent starts but Kubernetes events are not appearing
- Check that `KUBECONFIG` in `.env` points to the right file
- Run `kubectl get nodes` to confirm your cluster is reachable
- Check `logs/agent.log` for Kubernetes connection errors

### Diagnoser shows "running in fallback-only mode"
- Your `NIM_API_KEY` is not set or is empty
- The rule-based fallback still works, just less accurate
- Set `NIM_API_KEY=nvapi-...` in `.env` and restart

### Web UI shows blank page or 404
- The web UI build may have failed; run `./start.sh --rebuild`
- Check that `npm` is installed: `npm --version`

### Approvals not appearing in Slack
- Check `SLACK_BOT_TOKEN`, `SLACK_SIGNING_SECRET`, `SLACK_CHANNEL_ID` are all set
- Make sure the bot is invited to the channel: `/invite @AutoSRE` in Slack
- Check `logs/agent.log` for Slack API errors

### "circuit breaker tripped" in System Status
- More than 5 auto-actions fired within 5 minutes
- The system paused itself for safety
- Wait 5 minutes for the window to roll over, or restart the agent
- Investigate whether the remediations were correct before re-enabling

---

## Summary of All Environment Variables

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `KUBECONFIG` | Yes | `~/.kube/config` | Path to kube config |
| `GITOPS_REPO_PATH` | Yes | — | Local path to your GitOps repo |
| `NIM_API_KEY` | No | — | NVIDIA NIM API key for AI diagnosis |
| `NIM_MODEL` | No | `meta/llama-3.3-70b-instruct` | NIM model to use |
| `GEMINI_API_KEY` | No | — | Google Gemini key (fallback if no NIM) |
| `GIT_REMOTE_URL` | No | — | GitHub repo URL for pushing fixes |
| `GIT_TOKEN` | No | — | GitHub personal access token |
| `SLACK_BOT_TOKEN` | No | — | Slack bot token for notifications |
| `SLACK_SIGNING_SECRET` | No | — | Slack signing secret for webhooks |
| `SLACK_CHANNEL_ID` | No | — | Slack channel to post to |
| `PAGERDUTY_ROUTING_KEY` | No | — | PagerDuty routing key for escalation |
| `ORCHESTRATOR_APPLY_ENABLED` | No | `false` | Allow real GitOps commits |
| `ORCHESTRATOR_KILL_SWITCH` | No | `false` | Emergency stop all remediation |
| `REMEDIATION_DRY_RUN` | No | `true` | Dry-run all actions |
| `POLICY_FILE` | No | `./policy.yaml` | Path to policy rules |
| `DRY_RUN` | No | `true` | Global dry-run override |
| `LOG_LEVEL` | No | `info` | `debug`, `info`, `warn`, `error` |
| `WEBHOOK_ADDR` | No | `:8080` | Port for the main server |
| `DIAGNOSER_PORT` | No | `8001` | Port for the diagnoser |
| `LEARNER_PORT` | No | `8002` | Port for the learner |
| `AUDIT_FILE_PATH` | No | `./data/audit.jsonl` | Audit log file path |
| `STORE_DSN` | No | `file:./data/autosre.db` | SQLite database path |
| `LOKI_ADDR` | No | — | Loki URL for log-based detection |
| `API_OIDC_ENABLED` | No | `false` | Enable JWT auth on the API |
