"""Append-only JSONL outcome store.

Each call to append() writes one JSON line to the outcomes file.
load_all() re-reads from disk on every call — no caching needed at this scale.

There is intentionally no update() or delete() method.
# TODO: replace with a durable/WORM store (e.g. S3 object append or immutable Postgres table)
#        when durability requirements are formalised.
"""

from contextlib import suppress
from pathlib import Path

from pydantic import ValidationError

from .contracts import Outcome


class OutcomeStore:
    def __init__(self, path: str) -> None:
        self._path = Path(path)

    def append(self, outcome: Outcome) -> None:
        """Append outcome as a single JSON line. Creates parent dirs as needed."""
        self._path.parent.mkdir(parents=True, exist_ok=True)
        with self._path.open("a", encoding="utf-8") as f:
            f.write(outcome.model_dump_json() + "\n")

    def load_all(self) -> list[Outcome]:
        """Return all recorded outcomes. Returns [] if file does not exist."""
        if not self._path.exists():
            return []
        outcomes: list[Outcome] = []
        with self._path.open("r", encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                # Skip malformed lines — append-only files can have partial writes.
                with suppress(ValueError, ValidationError):
                    outcomes.append(Outcome.model_validate_json(line))
        return outcomes
