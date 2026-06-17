"""Diagnoser service entrypoint.

Usage:
    python -m diagnoser.main            # start HTTP server on DIAGNOSER_PORT
    python -m diagnoser.main --check    # verify config and exit
"""

import logging
import os
import sys

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s %(message)s",
)

logger = logging.getLogger(__name__)


def main() -> None:
    env = os.getenv("PYTHON_ENV", "development")
    port = int(os.getenv("DIAGNOSER_PORT", "8001"))
    host = os.getenv("DIAGNOSER_HOST", "0.0.0.0")

    # --check flag: validate config and exit without starting the server.
    if "--check" in sys.argv:
        nim_key = os.getenv("NIM_API_KEY", "")
        gemini_key = os.getenv("GEMINI_API_KEY", "")
        if nim_key:
            mode = f"nim ({os.getenv('NIM_MODEL', 'meta/llama-3.3-70b-instruct')})"
        elif gemini_key:
            mode = f"gemini ({os.getenv('GEMINI_MODEL', 'gemini-1.5-flash')})"
        else:
            mode = "fallback-only (rule-based)"
        print(f"AutoSRE Diagnoser config check OK (env={env}, mode={mode})")
        sys.exit(0)

    logger.info("AutoSRE Diagnoser starting (env=%s, port=%d)", env, port)
    nim_key = os.getenv("NIM_API_KEY", "")
    gemini_key = os.getenv("GEMINI_API_KEY", "")
    if nim_key:
        logger.info(
            "NIM_API_KEY is set; primary provider = NVIDIA NIM (model=%s)",
            os.getenv("NIM_MODEL", "meta/llama-3.3-70b-instruct"),
        )
    elif gemini_key:
        logger.info("GEMINI_API_KEY is set; primary provider = Gemini (legacy)")
    else:
        logger.warning(
            "No LLM API key found (NIM_API_KEY or GEMINI_API_KEY); "
            "running in fallback-only mode (rule-based provider)"
        )

    try:
        import uvicorn  # type: ignore[import]
    except ImportError:
        logger.error("uvicorn is not installed; cannot start HTTP server")
        sys.exit(1)

    from diagnoser.server import app  # noqa: PLC0415

    uvicorn.run(app, host=host, port=port, log_level="info")


if __name__ == "__main__":
    main()
