"""Pydantic models shared between the learner service modules."""

from datetime import datetime
from typing import Optional

from pydantic import BaseModel, Field


class Outcome(BaseModel):
    """A completed remediation pipeline run posted by the Go agent."""

    incident_id: str = Field(..., description="Correlator-assigned incident identifier")
    trace_id: str = Field(..., description="TraceID linking this outcome to its audit trail")
    failure_mode: str = Field(..., description="What the diagnoser determined (e.g. OOMKilled)")
    proposed_action: str = Field(..., description="What the diagnoser suggested (e.g. bump-memory-limit)")
    verdict: str = Field(..., description="Policy decision: AUTO | REQUIRE_APPROVAL | BLOCK")
    applied: bool = Field(..., description="True if action.Apply() was called and succeeded")
    verification_outcome: str = Field(
        default="",
        description="RECOVERED | FAILED | INCONCLUSIVE | '' (empty when pipeline did not reach verify stage)",
    )
    timestamp: datetime = Field(..., description="When the pipeline completed (UTC)")


class FailureModeActionStats(BaseModel):
    """Aggregated success-rate statistics for one (failure_mode, action) pair."""

    attempts: int = Field(..., ge=0)
    recovered: int = Field(..., ge=0)
    failed: int = Field(..., ge=0)
    inconclusive: int = Field(..., ge=0)
    success_rate: float = Field(..., ge=0.0, le=1.0, description="recovered / attempts")


class StatsResponse(BaseModel):
    """Read-only advisory stats returned by GET /stats.

    These stats are for observability only. They are never fed back into
    the policy engine or diagnosis layer to alter control flow.
    """

    updated_at: Optional[datetime] = Field(
        None,
        description="When the stats were computed (None if no outcomes recorded yet)",
    )
    total_outcomes: int = Field(..., ge=0)
    by_failure_mode_action: dict[str, FailureModeActionStats] = Field(
        default_factory=dict,
        description="Keys are 'failure_mode/proposed_action' (e.g. 'OOMKilled/bump-memory-limit')",
    )
