@echo off
REM =============================================================================
REM AutoSRE — One-command startup script (Windows)
REM Usage:  start.bat
REM         start.bat stop     (kill running services)
REM =============================================================================

setlocal EnableDelayedExpansion
set "PROJECT_ROOT=%~dp0"
REM Strip trailing backslash
if "%PROJECT_ROOT:~-1%"=="\" set "PROJECT_ROOT=%PROJECT_ROOT:~0,-1%"

set "PIDS_DIR=%PROJECT_ROOT%\.run"
set "LOG_DIR=%PROJECT_ROOT%\logs"
set "DATA_DIR=%PROJECT_ROOT%\data"
set "AGENT_BIN=%PROJECT_ROOT%\agent\autosre.exe"
set "DIAGNOSER_VENV=%PROJECT_ROOT%\diagnoser\.venv"
set "LEARNER_VENV=%PROJECT_ROOT%\learner\.venv"
set "WEB_UI_DIST=%PROJECT_ROOT%\web-ui\dist"

REM ---- --stop ----------------------------------------------------------------
if /i "%1"=="stop" goto :stop

REM ---- startup ---------------------------------------------------------------
echo.
echo  ==============================================
echo    AutoSRE -- Autonomous GitOps and Auto-Fix
echo  ==============================================
echo.

REM Create directories
if not exist "%PIDS_DIR%" mkdir "%PIDS_DIR%"
if not exist "%LOG_DIR%"  mkdir "%LOG_DIR%"
if not exist "%DATA_DIR%" mkdir "%DATA_DIR%"

REM Load .env variables (Windows-safe line-by-line parse)
if exist "%PROJECT_ROOT%\.env" (
    echo [autosre] Loading config from .env
    for /f "usebackq tokens=1,* delims==" %%A in ("%PROJECT_ROOT%\.env") do (
        set "line=%%A"
        if not "!line:~0,1!"=="#" (
            if not "%%A"=="" if not "%%B"=="" (
                set "%%A=%%B"
            )
        )
    )
) else (
    echo [autosre] WARNING: .env not found -- using defaults
)

REM Set defaults
if "%LEARNER_PORT%"==""    set LEARNER_PORT=8002
if "%LEARNER_HOST%"==""    set LEARNER_HOST=127.0.0.1
if "%DIAGNOSER_PORT%"==""  set DIAGNOSER_PORT=8001
if "%DIAGNOSER_HOST%"==""  set DIAGNOSER_HOST=127.0.0.1

REM ---- Preflight checks -------------------------------------------------------
echo [autosre] Running preflight checks...

REM Check agent binary (Windows uses .exe)
if not exist "%AGENT_BIN%" (
    REM Try without .exe (Linux build cross-compiled or WSL)
    set "AGENT_BIN=%PROJECT_ROOT%\agent\autosre"
    if not exist "!AGENT_BIN!" (
        echo [autosre] ERROR: Agent binary not found.
        echo [autosre]   Run setup.bat (or setup.sh in WSL/Git Bash) to build it.
        pause
        exit /b 1
    )
)

if not exist "%DIAGNOSER_VENV%\Scripts\python.exe" (
    if not exist "%DIAGNOSER_VENV%\bin\python3" (
        echo [autosre] ERROR: Diagnoser venv not found. Run setup.bat first.
        pause
        exit /b 1
    )
)

if not exist "%WEB_UI_DIST%\index.html" (
    echo [autosre] WARNING: Web UI dist not found. Dashboard will show placeholder.
    set "WEB_UI_DIR="
) else (
    set "WEB_UI_DIR=%WEB_UI_DIST%"
)

echo [autosre] Preflight checks passed.
echo.

REM ---- Start Learner (port 8002) ---------------------------------------------
echo [autosre] Starting Learner on port %LEARNER_PORT%...
set "LEARNER_STATS_PATH=%DATA_DIR%\outcomes.jsonl"

REM Try Windows venv path first, fall back to Linux path (WSL/Git Bash)
if exist "%LEARNER_VENV%\Scripts\python.exe" (
    set "LEARNER_PY=%LEARNER_VENV%\Scripts\python.exe"
) else (
    set "LEARNER_PY=%LEARNER_VENV%\bin\python3"
)

start "AutoSRE-Learner" /B "%LEARNER_PY%" -m learner.main >> "%LOG_DIR%\learner.log" 2>&1
timeout /t 2 /nobreak >nul
curl -sf http://localhost:%LEARNER_PORT%/healthz >nul 2>&1
if errorlevel 1 (
    echo [autosre] ERROR: Learner failed to start. Check %LOG_DIR%\learner.log
    pause
    exit /b 1
)
echo [autosre] Learner running  --^>  http://localhost:%LEARNER_PORT%

REM ---- Start Diagnoser (port 8001) ------------------------------------------
echo [autosre] Starting Diagnoser on port %DIAGNOSER_PORT%...

if exist "%DIAGNOSER_VENV%\Scripts\python.exe" (
    set "DIAGNOSER_PY=%DIAGNOSER_VENV%\Scripts\python.exe"
) else (
    set "DIAGNOSER_PY=%DIAGNOSER_VENV%\bin\python3"
)

start "AutoSRE-Diagnoser" /B "%DIAGNOSER_PY%" -m diagnoser.main >> "%LOG_DIR%\diagnoser.log" 2>&1
timeout /t 3 /nobreak >nul
curl -sf http://localhost:%DIAGNOSER_PORT%/healthz >nul 2>&1
if errorlevel 1 (
    echo [autosre] ERROR: Diagnoser failed to start. Check %LOG_DIR%\diagnoser.log
    pause
    exit /b 1
)
echo [autosre] Diagnoser running --^>  http://localhost:%DIAGNOSER_PORT%

REM ---- Start Agent (port 8080) -----------------------------------------------
echo [autosre] Starting Agent on port 8080...
cd /d "%PROJECT_ROOT%"
start "AutoSRE-Agent" /B "%AGENT_BIN%" run >> "%LOG_DIR%\agent.log" 2>&1
timeout /t 3 /nobreak >nul
curl -sf http://localhost:8080/api/v1/health >nul 2>&1
if errorlevel 1 (
    echo [autosre] ERROR: Agent failed to start. Check %LOG_DIR%\agent.log
    pause
    exit /b 1
)
echo [autosre] Agent running     --^>  http://localhost:8080

REM ---- Done ------------------------------------------------------------------
echo.
echo  ============================================================
echo    AutoSRE is running!
echo  ============================================================
echo    Web Dashboard  --^>  http://localhost:8080
echo    API Status     --^>  http://localhost:8080/api/v1/status
echo    Incidents      --^>  http://localhost:8080/api/v1/incidents
echo    Diagnoser      --^>  http://localhost:%DIAGNOSER_PORT%/healthz
echo    Learner        --^>  http://localhost:%LEARNER_PORT%/healthz
echo  ------------------------------------------------------------
echo    Logs           --^>  .\logs\
echo    Stop           --^>  start.bat stop
echo  ============================================================
echo.
echo  Press any key to open the dashboard in your browser...
pause >nul
start http://localhost:8080
goto :eof

REM ---- :stop -----------------------------------------------------------------
:stop
echo [autosre] Stopping AutoSRE services...
taskkill /FI "WINDOWTITLE eq AutoSRE-Agent"     /F >nul 2>&1 && echo [autosre] Agent stopped
taskkill /FI "WINDOWTITLE eq AutoSRE-Diagnoser" /F >nul 2>&1 && echo [autosre] Diagnoser stopped
taskkill /FI "WINDOWTITLE eq AutoSRE-Learner"   /F >nul 2>&1 && echo [autosre] Learner stopped
echo [autosre] Done.
goto :eof
