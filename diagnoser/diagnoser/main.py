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
        api_key = os.getenv("GEMINI_API_KEY", "")
        mode = "gemini" if api_key else "fallback-only"
        print(f"AutoSRE Diagnoser config check OK (env={env}, mode={mode})")
        sys.exit(0)

    logger.info("AutoSRE Diagnoser starting (env=%s, port=%d)", env, port)
    api_key = os.getenv("GEMINI_API_KEY", "")
    if not api_key:
        logger.warning(
            "GEMINI_API_KEY not set; running in fallback-only mode "
            "(all diagnoses will use the rule-based provider)"
        )
    else:
        logger.info("GEMINI_API_KEY is set; primary provider = Gemini")

    try:
        import uvicorn  # type: ignore[import]
    except ImportError:
        logger.error("uvicorn is not installed; cannot start HTTP server")
        sys.exit(1)

    from diagnoser.server import app  # noqa: PLC0415

    uvicorn.run(app, host=host, port=port, log_level="info")


if __name__ == "__main__":
    main()
