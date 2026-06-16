#!/usr/bin/env bash
# =============================================================================
# AutoSRE — One-command dependency installer
# Installs: Go 1.22, Python venvs (diagnoser + learner), Node modules, builds
# Usage:  ./setup.sh
# =============================================================================
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()    { echo -e "${CYAN}[setup]${NC} $*"; }
success() { echo -e "${GREEN}[setup]${NC} $*"; }
warn()    { echo -e "${YELLOW}[setup]${NC} $*"; }
error()   { echo -e "${RED}[setup]${NC} $*" >&2; }

echo ""
echo -e "${CYAN}╔══════════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║   AutoSRE Setup — Installing Dependencies    ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════════╝${NC}"
echo ""

# ---- 1. Go 1.22 -------------------------------------------------------------
GO_SDK="$HOME/go-sdk/go"
if [[ -x "$GO_SDK/bin/go" ]]; then
    GO_VER=$("$GO_SDK/bin/go" version | awk '{print $3}')
    success "Go already installed: $GO_VER"
else
    info "Installing Go 1.22.5..."
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64)  GOARCH="amd64" ;;
        aarch64) GOARCH="arm64" ;;
        *)        error "Unsupported arch: $ARCH"; exit 1 ;;
    esac
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    GO_PKG="go1.22.5.$OS-$GOARCH.tar.gz"
    GO_URL="https://go.dev/dl/$GO_PKG"
    mkdir -p "$HOME/go-sdk"
    curl -fsSL "$GO_URL" -o "/tmp/$GO_PKG"
    tar -C "$HOME/go-sdk" -xzf "/tmp/$GO_PKG"
    rm -f "/tmp/$GO_PKG"
    success "Go 1.22.5 installed to $HOME/go-sdk/go"
fi
export PATH="$GO_SDK/bin:$HOME/.local/bin:$PATH"
export GOPATH="$HOME/go"
export GOMODCACHE="$HOME/go/pkg/mod"

# ---- 2. Python pip / virtualenv ---------------------------------------------
info "Checking Python tooling..."
if ! python3 -m pip --version &>/dev/null 2>&1; then
    info "Installing pip..."
    curl -fsSL https://bootstrap.pypa.io/get-pip.py -o /tmp/get-pip.py
    python3 /tmp/get-pip.py --user --break-system-packages -q
    export PATH="$HOME/.local/bin:$PATH"
fi
if ! python3 -m virtualenv --version &>/dev/null 2>&1; then
    info "Installing virtualenv..."
    python3 -m pip install virtualenv -q --break-system-packages
fi
success "Python tooling ready ($(python3 --version))"

# ---- 3. Diagnoser venv ------------------------------------------------------
DIAGNOSER_VENV="$PROJECT_ROOT/diagnoser/.venv"
if [[ ! -f "$DIAGNOSER_VENV/bin/pip" ]]; then
    info "Creating diagnoser Python virtual environment..."
    python3 -m virtualenv "$DIAGNOSER_VENV" -q
fi
info "Installing diagnoser dependencies..."
"$DIAGNOSER_VENV/bin/pip" install -e "$PROJECT_ROOT/diagnoser" -q
success "Diagnoser deps installed"

# ---- 4. Learner venv --------------------------------------------------------
LEARNER_VENV="$PROJECT_ROOT/learner/.venv"
if [[ ! -f "$LEARNER_VENV/bin/pip" ]]; then
    info "Creating learner Python virtual environment..."
    python3 -m virtualenv "$LEARNER_VENV" -q
fi
info "Installing learner dependencies..."
"$LEARNER_VENV/bin/pip" install -e "$PROJECT_ROOT/learner" -q
success "Learner deps installed"

# ---- 5. Go module + binary --------------------------------------------------
info "Running go mod tidy..."
(cd "$PROJECT_ROOT/agent" && go mod tidy 2>&1 | grep -v "^go:" || true)
info "Building autosre binary..."
(cd "$PROJECT_ROOT/agent" && go build -o autosre ./cmd/autosre/)
success "autosre binary built: $PROJECT_ROOT/agent/autosre"

# ---- 6. Web UI --------------------------------------------------------------
if ! command -v npm &>/dev/null; then
    warn "npm not found — Web UI will not be built. Install Node.js 18+ for the dashboard."
else
    info "Installing Web UI npm packages..."
    (cd "$PROJECT_ROOT/web-ui" && npm install --silent)
    info "Building Web UI..."
    (cd "$PROJECT_ROOT/web-ui" && npm run build --silent)
    success "Web UI built: $PROJECT_ROOT/web-ui/dist"
fi

# ---- 7. Create .env if missing ----------------------------------------------
if [[ ! -f "$PROJECT_ROOT/.env" ]]; then
    info "Creating .env from .env.example..."
    cp "$PROJECT_ROOT/.env.example" "$PROJECT_ROOT/.env"
    # Apply safe local defaults
    sed -i 's|^AUDIT_FILE_PATH=.*|AUDIT_FILE_PATH=./data/audit.jsonl|' "$PROJECT_ROOT/.env"
    sed -i 's|^LEARNER_STATS_PATH=.*|LEARNER_STATS_PATH=./data/outcomes.jsonl|' "$PROJECT_ROOT/.env"
    success ".env created (edit it to add SLACK_BOT_TOKEN, GEMINI_API_KEY, etc.)"
fi

# ---- 8. Dirs ----------------------------------------------------------------
mkdir -p "$PROJECT_ROOT/data" "$PROJECT_ROOT/logs" "$PROJECT_ROOT/.run"

# ---- done -------------------------------------------------------------------
echo ""
echo -e "${GREEN}╔══════════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║  Setup complete!                                     ║${NC}"
echo -e "${GREEN}╠══════════════════════════════════════════════════════╣${NC}"
echo -e "${GREEN}║  Run the platform:  ./start.sh                       ║${NC}"
echo -e "${GREEN}║  Stop it:           ./start.sh --stop                ║${NC}"
echo -e "${GREEN}║  Edit config:       .env                             ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════════════════╝${NC}"
echo ""
