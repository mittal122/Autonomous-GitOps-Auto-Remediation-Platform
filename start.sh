#!/usr/bin/env bash
# =============================================================================
# AutoSRE — One-command startup script (Linux / macOS)
#
# Usage:
#   ./start.sh              Start all services (creates venvs + installs deps
#                           automatically on first run)
#   ./start.sh --stop       Kill all running services
#   ./start.sh --status     Show which services are running
#   ./start.sh --rebuild    Force-reinstall all deps and rebuild binaries
#   ./start.sh --logs       Tail live logs from all services
# =============================================================================
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PIDS_DIR="$PROJECT_ROOT/.run"
LOG_DIR="$PROJECT_ROOT/logs"
DATA_DIR="$PROJECT_ROOT/data"

AGENT_BIN="$PROJECT_ROOT/agent/autosre"
DIAGNOSER_DIR="$PROJECT_ROOT/diagnoser"
LEARNER_DIR="$PROJECT_ROOT/learner"
DIAGNOSER_VENV="$DIAGNOSER_DIR/.venv"
LEARNER_VENV="$LEARNER_DIR/.venv"
WEB_UI_DIR="$PROJECT_ROOT/web-ui"
WEB_UI_DIST="$WEB_UI_DIR/dist"

FORCE_REBUILD="${FORCE_REBUILD:-false}"
[[ "${1:-}" == "--rebuild" ]] && FORCE_REBUILD=true

# ── colour helpers ────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BLUE='\033[0;34m'; NC='\033[0m'
info()    { echo -e "${CYAN}[autosre]${NC} $*"; }
success() { echo -e "${GREEN}[autosre]${NC} $*"; }
warn()    { echo -e "${YELLOW}[autosre]${NC} $*"; }
error()   { echo -e "${RED}[autosre]${NC} $*" >&2; }
step()    { echo -e "${BLUE}[autosre]${NC} $*"; }

# ── --stop ────────────────────────────────────────────────────────────────────
if [[ "${1:-}" == "--stop" ]]; then
    info "Stopping all AutoSRE services..."
    stopped=0
    for svc in agent diagnoser learner; do
        pidfile="$PIDS_DIR/$svc.pid"
        if [[ -f "$pidfile" ]]; then
            pid=$(cat "$pidfile")
            if kill -0 "$pid" 2>/dev/null; then
                kill "$pid"
                success "  Stopped $svc (pid $pid)"
                (( stopped++ )) || true
            else
                warn "  $svc pid file stale (process already gone)"
            fi
            rm -f "$pidfile"
        else
            warn "  $svc is not running"
        fi
    done
    [[ $stopped -eq 0 ]] && warn "No services were running." || success "All services stopped."
    exit 0
fi

# ── --status ──────────────────────────────────────────────────────────────────
if [[ "${1:-}" == "--status" ]]; then
    echo ""
    echo -e "${CYAN}AutoSRE Service Status${NC}"
    echo "────────────────────────────"
    for svc in agent diagnoser learner; do
        pidfile="$PIDS_DIR/$svc.pid"
        if [[ -f "$pidfile" ]]; then
            pid=$(cat "$pidfile")
            if kill -0 "$pid" 2>/dev/null; then
                echo -e "  ${GREEN}●${NC} $svc   running (pid $pid)"
            else
                echo -e "  ${RED}●${NC} $svc   dead (stale pid $pid)"
            fi
        else
            echo -e "  ${YELLOW}●${NC} $svc   not running"
        fi
    done
    echo ""
    exit 0
fi

# ── --logs ────────────────────────────────────────────────────────────────────
if [[ "${1:-}" == "--logs" ]]; then
    if [[ ! -d "$LOG_DIR" ]]; then
        error "Log directory not found. Start the services first."
        exit 1
    fi
    exec tail -f "$LOG_DIR/agent.log" "$LOG_DIR/diagnoser.log" "$LOG_DIR/learner.log" 2>/dev/null
fi

# ══════════════════════════════════════════════════════════════════════════════
# STARTUP
# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo -e "${CYAN}╔══════════════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║   AutoSRE — Autonomous GitOps & Auto-Remediation ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════════════╝${NC}"
echo ""

mkdir -p "$PIDS_DIR" "$LOG_DIR" "$DATA_DIR"

# ── Load .env ─────────────────────────────────────────────────────────────────
ENV_FILE="$PROJECT_ROOT/.env"
if [[ -f "$ENV_FILE" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$ENV_FILE"
    set +a
    success "Loaded config from .env"
else
    warn ".env not found — using defaults  (tip: cp .env.example .env)"
fi

# ── helpers ───────────────────────────────────────────────────────────────────

# Return md5 hash of a file (portable: tries md5sum then md5)
file_hash() {
    if command -v md5sum &>/dev/null; then
        md5sum "$1" | cut -d' ' -f1
    elif command -v md5 &>/dev/null; then
        md5 -q "$1"
    else
        echo "nohash"
    fi
}

# Locate python3 (3.11+)
find_python() {
    for py in python3.11 python3.12 python3.13 python3; do
        if command -v "$py" &>/dev/null; then
            ver=$("$py" -c 'import sys; print(sys.version_info.minor)' 2>/dev/null)
            if [[ "${ver:-0}" -ge 11 ]]; then
                echo "$py"
                return 0
            fi
        fi
    done
    return 1
}

# Locate go binary (local SDK or system)
find_go() {
    if [[ -x "$HOME/go-sdk/go/bin/go" ]]; then
        echo "$HOME/go-sdk/go/bin/go"
    elif command -v go &>/dev/null; then
        echo "go"
    else
        return 1
    fi
}

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 1 — Python isolated venvs
# ══════════════════════════════════════════════════════════════════════════════

setup_python_venv() {
    local name="$1"       # human label  (diagnoser | learner)
    local svc_dir="$2"    # absolute path to the service directory
    local venv="$svc_dir/.venv"
    local pyproject="$svc_dir/pyproject.toml"
    local marker="$venv/.deps_hash"

    step "── $name venv ──────────────────────────────────────"

    # 1. Create venv if it does not exist
    if [[ ! -f "$venv/bin/python3" ]]; then
        info "  venv not found — creating isolated environment..."
        local py
        if ! py=$(find_python); then
            error "  Python 3.11+ is required but not found."
            error "  Install it with: sudo apt install python3.11  (or brew install python@3.11)"
            exit 1
        fi
        info "  Using Python: $py ($("$py" --version))"
        "$py" -m venv "$venv"
        success "  venv created at $venv"
    else
        info "  venv exists at $venv — skipping creation"
    fi

    # 2. Upgrade pip silently (once per venv creation or rebuild)
    local pip_marker="$venv/.pip_upgraded"
    if [[ ! -f "$pip_marker" ]] || [[ "$FORCE_REBUILD" == "true" ]]; then
        info "  Upgrading pip..."
        "$venv/bin/pip" install --quiet --upgrade pip
        touch "$pip_marker"
    fi

    # 3. Install / update dependencies if pyproject.toml changed or --rebuild
    local current_hash
    current_hash=$(file_hash "$pyproject")
    local stored_hash=""
    [[ -f "$marker" ]] && stored_hash=$(cat "$marker")

    if [[ "$current_hash" != "$stored_hash" ]] || [[ "$FORCE_REBUILD" == "true" ]]; then
        info "  Installing/updating $name dependencies..."
        "$venv/bin/pip" install --quiet -e "$svc_dir[dev]"
        echo "$current_hash" > "$marker"
        success "  $name dependencies installed"
    else
        success "  $name dependencies already up to date"
    fi
}

setup_python_venv "diagnoser" "$DIAGNOSER_DIR"
echo ""
setup_python_venv "learner"   "$LEARNER_DIR"
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 2 — Go agent binary
# ══════════════════════════════════════════════════════════════════════════════

step "── agent binary ─────────────────────────────────────"

GO_BIN=""
if ! GO_BIN=$(find_go); then
    error "  Go is not installed."
    error "  Install from https://go.dev/dl/ or run: ./setup.sh"
    exit 1
fi
export PATH="$(dirname "$GO_BIN"):$PATH"
export GOPATH="$HOME/go"
export GOMODCACHE="$HOME/go/pkg/mod"

AGENT_HASH_MARKER="$PROJECT_ROOT/agent/.build_hash"
# Hash the go.mod + go.sum to detect if a rebuild is needed
AGENT_SRC_HASH=$(file_hash "$PROJECT_ROOT/agent/go.mod")

if [[ ! -x "$AGENT_BIN" ]]; then
    info "  Binary not found — building agent..."
    BUILD_NEEDED=true
elif [[ "$FORCE_REBUILD" == "true" ]]; then
    info "  --rebuild flag set — rebuilding agent..."
    BUILD_NEEDED=true
elif [[ ! -f "$AGENT_HASH_MARKER" ]] || [[ "$(cat "$AGENT_HASH_MARKER")" != "$AGENT_SRC_HASH" ]]; then
    info "  go.mod changed — rebuilding agent..."
    BUILD_NEEDED=true
else
    BUILD_NEEDED=false
fi

if [[ "$BUILD_NEEDED" == "true" ]]; then
    (
        cd "$PROJECT_ROOT/agent"
        "$GO_BIN" mod tidy
        "$GO_BIN" build -o autosre ./cmd/autosre/
    )
    echo "$AGENT_SRC_HASH" > "$AGENT_HASH_MARKER"
    success "  Agent built: $AGENT_BIN"
else
    success "  Agent binary up to date"
fi
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 3 — Web UI
# ══════════════════════════════════════════════════════════════════════════════

step "── web UI ───────────────────────────────────────────"

WEB_HASH_MARKER="$WEB_UI_DIR/.build_hash"
WEB_SRC_HASH=$(file_hash "$WEB_UI_DIR/package.json" 2>/dev/null || echo "nohash")

if [[ ! -d "$WEB_UI_DIST" ]]; then
    WEB_BUILD_NEEDED=true
    info "  dist/ not found — building web UI..."
elif [[ "$FORCE_REBUILD" == "true" ]]; then
    WEB_BUILD_NEEDED=true
    info "  --rebuild flag set — rebuilding web UI..."
elif [[ ! -f "$WEB_HASH_MARKER" ]] || [[ "$(cat "$WEB_HASH_MARKER")" != "$WEB_SRC_HASH" ]]; then
    WEB_BUILD_NEEDED=true
    info "  package.json changed — rebuilding web UI..."
else
    WEB_BUILD_NEEDED=false
fi

if [[ "$WEB_BUILD_NEEDED" == "true" ]]; then
    if ! command -v npm &>/dev/null; then
        warn "  npm not found — skipping web UI build (API-only mode)"
        WEB_UI_DIST=""
    else
        (
            cd "$WEB_UI_DIR"
            # Install node_modules only if missing or package.json changed
            if [[ ! -d node_modules ]] || [[ "$FORCE_REBUILD" == "true" ]]; then
                info "  Installing npm packages..."
                npm install --silent
            fi
            info "  Building web UI..."
            npm run build --silent
        )
        echo "$WEB_SRC_HASH" > "$WEB_HASH_MARKER"
        success "  Web UI built: $WEB_UI_DIST"
    fi
else
    success "  Web UI build up to date"
fi
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# SECTION 4 — Start services
# ══════════════════════════════════════════════════════════════════════════════

wait_healthy() {
    local url="$1"
    local name="$2"
    local retries="${3:-15}"
    for i in $(seq 1 "$retries"); do
        if curl -sf "$url" >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    error "$name did not become healthy after $retries seconds."
    error "  Check $LOG_DIR/$name.log for details."
    return 1
}

# ── learner (port 8002) ───────────────────────────────────────────────────────
LEARNER_PORT="${LEARNER_PORT:-8002}"
step "── starting learner on port $LEARNER_PORT ──────────────"
LEARNER_STATS_PATH="${LEARNER_STATS_PATH:-$DATA_DIR/outcomes.jsonl}" \
LEARNER_PORT="$LEARNER_PORT" \
LEARNER_HOST="${LEARNER_HOST:-127.0.0.1}" \
LOG_LEVEL="${LOG_LEVEL:-info}" \
"$LEARNER_VENV/bin/python3" -m learner.main \
    >> "$LOG_DIR/learner.log" 2>&1 &
echo $! > "$PIDS_DIR/learner.pid"

if wait_healthy "http://localhost:$LEARNER_PORT/healthz" "learner"; then
    success "  Learner running → http://localhost:$LEARNER_PORT"
else
    exit 1
fi
echo ""

# ── diagnoser (port 8001) ─────────────────────────────────────────────────────
DIAGNOSER_PORT="${DIAGNOSER_PORT:-8001}"
step "── starting diagnoser on port $DIAGNOSER_PORT ─────────────"
NIM_API_KEY="${NIM_API_KEY:-}" \
NIM_MODEL="${NIM_MODEL:-meta/llama-3.3-70b-instruct}" \
GEMINI_API_KEY="${GEMINI_API_KEY:-}" \
GEMINI_MODEL="${GEMINI_MODEL:-gemini-1.5-flash}" \
LLM_TIMEOUT_SECONDS="${LLM_TIMEOUT_SECONDS:-30}" \
DIAGNOSER_PORT="$DIAGNOSER_PORT" \
DIAGNOSER_HOST="${DIAGNOSER_HOST:-127.0.0.1}" \
LOG_LEVEL="${LOG_LEVEL:-info}" \
"$DIAGNOSER_VENV/bin/python3" -m diagnoser.main \
    >> "$LOG_DIR/diagnoser.log" 2>&1 &
echo $! > "$PIDS_DIR/diagnoser.pid"

if wait_healthy "http://localhost:$DIAGNOSER_PORT/healthz" "diagnoser"; then
    success "  Diagnoser running → http://localhost:$DIAGNOSER_PORT"
    if [[ -n "${NIM_API_KEY:-}" ]]; then
        success "  LLM provider → NVIDIA NIM (${NIM_MODEL:-meta/llama-3.3-70b-instruct})"
    elif [[ -n "${GEMINI_API_KEY:-}" ]]; then
        success "  LLM provider → Google Gemini (legacy)"
    else
        warn "  LLM provider → rule-based fallback (set NIM_API_KEY for AI diagnosis)"
    fi
else
    exit 1
fi
echo ""

# ── agent (port 8080) ─────────────────────────────────────────────────────────
step "── starting agent on port 8080 ────────────────────────"
cd "$PROJECT_ROOT"
WEB_UI_DIR="${WEB_UI_DIST:-}" \
DIAGNOSER_ADDR="${DIAGNOSER_ADDR:-http://127.0.0.1:$DIAGNOSER_PORT}" \
LEARNER_ADDR="${LEARNER_ADDR:-http://127.0.0.1:$LEARNER_PORT}" \
STORE_DSN="${STORE_DSN:-file:$DATA_DIR/autosre.db?_journal_mode=WAL}" \
AUDIT_ENABLED="${AUDIT_ENABLED:-true}" \
AUDIT_FILE_PATH="${AUDIT_FILE_PATH:-$DATA_DIR/audit.jsonl}" \
GITOPS_REPO_PATH="${GITOPS_REPO_PATH:-$PROJECT_ROOT/gitops}" \
WEBHOOK_ADDR="${WEBHOOK_ADDR:-:8080}" \
"$AGENT_BIN" run \
    >> "$LOG_DIR/agent.log" 2>&1 &
echo $! > "$PIDS_DIR/agent.pid"

if wait_healthy "http://localhost:8080/api/v1/health" "agent"; then
    success "  Agent running → http://localhost:8080"
else
    exit 1
fi

# ══════════════════════════════════════════════════════════════════════════════
# DONE
# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo -e "${GREEN}╔══════════════════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║  AutoSRE is running!                                         ║${NC}"
echo -e "${GREEN}╠══════════════════════════════════════════════════════════════╣${NC}"
echo -e "${GREEN}║  Web Dashboard  →  http://localhost:8080                     ║${NC}"
echo -e "${GREEN}║  API Health     →  http://localhost:8080/api/v1/health       ║${NC}"
echo -e "${GREEN}║  Incidents      →  http://localhost:8080/api/v1/incidents    ║${NC}"
echo -e "${GREEN}║  Metrics        →  http://localhost:8080/metrics             ║${NC}"
echo -e "${GREEN}║  Diagnoser      →  http://localhost:${DIAGNOSER_PORT}/healthz              ║${NC}"
echo -e "${GREEN}║  Learner        →  http://localhost:${LEARNER_PORT}/healthz               ║${NC}"
echo -e "${GREEN}╠══════════════════════════════════════════════════════════════╣${NC}"
echo -e "${GREEN}║  Logs    →  ./start.sh --logs                                ║${NC}"
echo -e "${GREEN}║  Status  →  ./start.sh --status                              ║${NC}"
echo -e "${GREEN}║  Stop    →  ./start.sh --stop                                ║${NC}"
echo -e "${GREEN}║  Rebuild →  ./start.sh --rebuild                             ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════════════════════════╝${NC}"
echo ""
