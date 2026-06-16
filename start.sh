#!/usr/bin/env bash
# =============================================================================
# AutoSRE — One-command startup script (Linux / macOS)
# Usage:  ./start.sh
#         ./start.sh --stop      (kill running services)
#         ./start.sh --status    (check what is running)
# =============================================================================
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PIDS_DIR="$PROJECT_ROOT/.run"
LOG_DIR="$PROJECT_ROOT/logs"
DATA_DIR="$PROJECT_ROOT/data"
AGENT_BIN="$PROJECT_ROOT/agent/autosre"
DIAGNOSER_VENV="$PROJECT_ROOT/diagnoser/.venv"
LEARNER_VENV="$PROJECT_ROOT/learner/.venv"
WEB_UI_DIST="$PROJECT_ROOT/web-ui/dist"

# ---- colour helpers ---------------------------------------------------------
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()    { echo -e "${CYAN}[autosre]${NC} $*"; }
success() { echo -e "${GREEN}[autosre]${NC} $*"; }
warn()    { echo -e "${YELLOW}[autosre]${NC} $*"; }
error()   { echo -e "${RED}[autosre]${NC} $*" >&2; }

# ---- --stop -----------------------------------------------------------------
if [[ "${1:-}" == "--stop" ]]; then
    info "Stopping all AutoSRE services..."
    for svc in agent diagnoser learner; do
        pidfile="$PIDS_DIR/$svc.pid"
        if [[ -f "$pidfile" ]]; then
            pid=$(cat "$pidfile")
            if kill -0 "$pid" 2>/dev/null; then
                kill "$pid" && success "Stopped $svc (pid $pid)"
            fi
            rm -f "$pidfile"
        fi
    done
    exit 0
fi

# ---- --status ---------------------------------------------------------------
if [[ "${1:-}" == "--status" ]]; then
    for svc in agent diagnoser learner; do
        pidfile="$PIDS_DIR/$svc.pid"
        if [[ -f "$pidfile" ]]; then
            pid=$(cat "$pidfile")
            if kill -0 "$pid" 2>/dev/null; then
                success "$svc is running (pid $pid)"
            else
                warn "$svc pid file exists but process is dead"
            fi
        else
            warn "$svc is NOT running"
        fi
    done
    exit 0
fi

# ---- startup ----------------------------------------------------------------
echo ""
echo -e "${CYAN}╔══════════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║   AutoSRE — Autonomous GitOps & Auto-Fix     ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════════╝${NC}"
echo ""

mkdir -p "$PIDS_DIR" "$LOG_DIR" "$DATA_DIR"

# Load .env if present
ENV_FILE="$PROJECT_ROOT/.env"
if [[ -f "$ENV_FILE" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$ENV_FILE"
    set +a
    info "Loaded config from .env"
else
    warn ".env not found — using defaults (run: cp .env.example .env)"
fi

# ---- preflight checks -------------------------------------------------------
info "Running preflight checks..."

# Go agent binary
if [[ ! -x "$AGENT_BIN" ]]; then
    warn "Agent binary not found — building now..."
    if ! command -v go &>/dev/null; then
        # Try the local Go SDK installed by setup
        if [[ -x "$HOME/go-sdk/go/bin/go" ]]; then
            export PATH="$HOME/go-sdk/go/bin:$PATH"
        else
            error "Go is not installed. Run: setup.sh to install dependencies."
            exit 1
        fi
    fi
    export GOPATH="$HOME/go"
    export GOMODCACHE="$HOME/go/pkg/mod"
    (cd "$PROJECT_ROOT/agent" && go mod tidy && go build -o autosre ./cmd/autosre/)
    success "Agent binary built"
fi

# Python venvs
if [[ ! -f "$DIAGNOSER_VENV/bin/python3" ]]; then
    error "Diagnoser venv missing. Run: ./setup.sh to install dependencies."
    exit 1
fi
if [[ ! -f "$LEARNER_VENV/bin/python3" ]]; then
    error "Learner venv missing. Run: ./setup.sh to install dependencies."
    exit 1
fi

# Web UI dist
if [[ ! -d "$WEB_UI_DIST" ]]; then
    warn "Web UI not built — building now..."
    if ! command -v npm &>/dev/null; then
        error "npm is not installed. Install Node.js 18+ first."
        exit 1
    fi
    (cd "$PROJECT_ROOT/web-ui" && npm install --silent && npm run build --silent)
    success "Web UI built"
fi

success "Preflight checks passed"
echo ""

# ---- start learner (port 8002) ----------------------------------------------
info "Starting Learner service on port ${LEARNER_PORT:-8002}..."
LEARNER_STATS_PATH="${LEARNER_STATS_PATH:-$DATA_DIR/outcomes.jsonl}" \
LEARNER_PORT="${LEARNER_PORT:-8002}" \
LEARNER_HOST="${LEARNER_HOST:-127.0.0.1}" \
LOG_LEVEL="${LOG_LEVEL:-info}" \
"$LEARNER_VENV/bin/python3" -m learner.main \
    >> "$LOG_DIR/learner.log" 2>&1 &
echo $! > "$PIDS_DIR/learner.pid"
sleep 1
if curl -sf http://localhost:${LEARNER_PORT:-8002}/healthz >/dev/null 2>&1; then
    success "Learner started → http://localhost:${LEARNER_PORT:-8002}"
else
    error "Learner failed to start. Check $LOG_DIR/learner.log"
    exit 1
fi

# ---- start diagnoser (port 8001) --------------------------------------------
info "Starting Diagnoser service on port ${DIAGNOSER_PORT:-8001}..."
DIAGNOSER_PORT="${DIAGNOSER_PORT:-8001}" \
DIAGNOSER_HOST="${DIAGNOSER_HOST:-127.0.0.1}" \
GEMINI_API_KEY="${GEMINI_API_KEY:-}" \
GEMINI_MODEL="${GEMINI_MODEL:-gemini-1.5-flash}" \
LLM_TIMEOUT_SECONDS="${LLM_TIMEOUT_SECONDS:-30}" \
LOG_LEVEL="${LOG_LEVEL:-info}" \
"$DIAGNOSER_VENV/bin/python3" -m diagnoser.main \
    >> "$LOG_DIR/diagnoser.log" 2>&1 &
echo $! > "$PIDS_DIR/diagnoser.pid"
sleep 2
if curl -sf http://localhost:${DIAGNOSER_PORT:-8001}/healthz >/dev/null 2>&1; then
    success "Diagnoser started → http://localhost:${DIAGNOSER_PORT:-8001}"
    if [[ -z "${GEMINI_API_KEY:-}" ]]; then
        warn "  GEMINI_API_KEY not set → running in rule-based fallback mode (no LLM)"
    fi
else
    error "Diagnoser failed to start. Check $LOG_DIR/diagnoser.log"
    exit 1
fi

# ---- start agent (port 8080) ------------------------------------------------
info "Starting Agent on port ${WEBHOOK_ADDR:-:8080}..."
cd "$PROJECT_ROOT"
WEB_UI_DIR="$WEB_UI_DIST" \
"$AGENT_BIN" run \
    >> "$LOG_DIR/agent.log" 2>&1 &
echo $! > "$PIDS_DIR/agent.pid"
sleep 2
if curl -sf http://localhost:8080/api/v1/health >/dev/null 2>&1; then
    success "Agent started → http://localhost:8080"
else
    error "Agent failed to start. Check $LOG_DIR/agent.log"
    exit 1
fi

# ---- done -------------------------------------------------------------------
echo ""
echo -e "${GREEN}╔══════════════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║  AutoSRE is running!                                     ║${NC}"
echo -e "${GREEN}╠══════════════════════════════════════════════════════════╣${NC}"
echo -e "${GREEN}║  Web Dashboard  →  http://localhost:8080                 ║${NC}"
echo -e "${GREEN}║  API Status     →  http://localhost:8080/api/v1/status   ║${NC}"
echo -e "${GREEN}║  Incidents      →  http://localhost:8080/api/v1/incidents║${NC}"
echo -e "${GREEN}║  Diagnoser      →  http://localhost:${DIAGNOSER_PORT:-8001}/healthz              ║${NC}"
echo -e "${GREEN}║  Learner        →  http://localhost:${LEARNER_PORT:-8002}/healthz               ║${NC}"
echo -e "${GREEN}╠══════════════════════════════════════════════════════════╣${NC}"
echo -e "${GREEN}║  Logs   →  ./logs/   (agent.log, diagnoser.log, ...)     ║${NC}"
echo -e "${GREEN}║  Stop   →  ./start.sh --stop                             ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════════════════════╝${NC}"
echo ""
