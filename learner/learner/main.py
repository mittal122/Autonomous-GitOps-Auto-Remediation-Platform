"""Entrypoint for the AutoSRE Learner service."""

import os

import uvicorn

from .server import app


def main() -> None:
    host = os.getenv("LEARNER_HOST", "0.0.0.0")
    port = int(os.getenv("LEARNER_PORT", "8002"))
    log_level = os.getenv("LOG_LEVEL", "info").lower()
    uvicorn.run(app, host=host, port=port, log_level=log_level)


if __name__ == "__main__":
    main()
