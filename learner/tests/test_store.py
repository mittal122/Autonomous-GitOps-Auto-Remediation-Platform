"""Tests for OutcomeStore — append-only JSONL persistence."""

import pytest
from datetime import datetime, timezone

from learner.contracts import Outcome
from learner.store import OutcomeStore


def make_outcome(incident_id: str, failure_mode: str = "OOMKilled", applied: bool = True,
                 verification_outcome: str = "RECOVERED") -> Outcome:
    return Outcome(
        incident_id=incident_id,
        trace_id=f"trace-{incident_id}",
        failure_mode=failure_mode,
        proposed_action="bump-memory-limit",
        verdict="AUTO",
        applied=applied,
        verification_outcome=verification_outcome,
        timestamp=datetime(2026, 6, 16, 10, 0, 0, tzinfo=timezone.utc),
    )


def test_append_and_load(tmp_path):
    store = OutcomeStore(str(tmp_path / "outcomes.jsonl"))
    store.append(make_outcome("inc-1"))
    store.append(make_outcome("inc-2"))

    loaded = store.load_all()
    assert len(loaded) == 2
    assert loaded[0].incident_id == "inc-1"
    assert loaded[1].incident_id == "inc-2"


def test_load_empty_returns_empty_list(tmp_path):
    store = OutcomeStore(str(tmp_path / "outcomes.jsonl"))
    assert store.load_all() == []


def test_load_nonexistent_file_returns_empty_list(tmp_path):
    store = OutcomeStore(str(tmp_path / "no_such_file.jsonl"))
    assert store.load_all() == []


def test_append_creates_parent_directories(tmp_path):
    store = OutcomeStore(str(tmp_path / "nested" / "deep" / "outcomes.jsonl"))
    store.append(make_outcome("inc-1"))
    loaded = store.load_all()
    assert len(loaded) == 1


def test_append_is_additive_across_instances(tmp_path):
    path = str(tmp_path / "outcomes.jsonl")
    store1 = OutcomeStore(path)
    store1.append(make_outcome("inc-1"))

    store2 = OutcomeStore(path)
    store2.append(make_outcome("inc-2"))

    store3 = OutcomeStore(path)
    loaded = store3.load_all()
    assert len(loaded) == 2


def test_load_skips_malformed_lines(tmp_path):
    path = tmp_path / "outcomes.jsonl"
    valid = make_outcome("inc-ok")
    path.write_text(valid.model_dump_json() + "\n{bad json\n" + valid.model_dump_json() + "\n")

    store = OutcomeStore(str(path))
    loaded = store.load_all()
    assert len(loaded) == 2
    assert all(o.incident_id == "inc-ok" for o in loaded)


def test_no_update_or_delete_methods():
    """OutcomeStore must not expose any update/delete methods — append-only contract."""
    store = OutcomeStore("/dev/null")
    assert not hasattr(store, "update")
    assert not hasattr(store, "delete")
    assert not hasattr(store, "truncate")
    assert not hasattr(store, "clear")
