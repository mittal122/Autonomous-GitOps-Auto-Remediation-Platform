"""Computes per-(failure_mode, action) success-rate statistics from a list of outcomes.

Stats are advisory and read-only. This module never writes to any file or modifies
any shared state; it is a pure function of the input list.

// TODO: add an opt-in confidence-hint field (e.g. Wilson score lower bound) once
//        the stats have been used in production for >= 100 outcomes per key.
"""

from datetime import datetime, timezone

from .contracts import FailureModeActionStats, Outcome, StatsResponse


def compute_stats(outcomes: list[Outcome]) -> StatsResponse:
    """Aggregate outcomes into per-(failure_mode/proposed_action) success-rate stats."""
    if not outcomes:
        return StatsResponse(updated_at=None, total_outcomes=0, by_failure_mode_action={})

    raw: dict[str, dict[str, int]] = {}

    for o in outcomes:
        key = f"{o.failure_mode}/{o.proposed_action}"
        if key not in raw:
            raw[key] = {"attempts": 0, "recovered": 0, "failed": 0, "inconclusive": 0}
        raw[key]["attempts"] += 1
        vo = o.verification_outcome.upper()
        if vo == "RECOVERED":
            raw[key]["recovered"] += 1
        elif vo == "FAILED":
            raw[key]["failed"] += 1
        elif vo == "INCONCLUSIVE":
            raw[key]["inconclusive"] += 1

    by_key: dict[str, FailureModeActionStats] = {}
    for key, counts in raw.items():
        attempts = counts["attempts"]
        success_rate = round(counts["recovered"] / attempts, 4) if attempts > 0 else 0.0
        by_key[key] = FailureModeActionStats(
            attempts=attempts,
            recovered=counts["recovered"],
            failed=counts["failed"],
            inconclusive=counts["inconclusive"],
            success_rate=success_rate,
        )

    return StatsResponse(
        updated_at=datetime.now(tz=timezone.utc),
        total_outcomes=len(outcomes),
        by_failure_mode_action=by_key,
    )
