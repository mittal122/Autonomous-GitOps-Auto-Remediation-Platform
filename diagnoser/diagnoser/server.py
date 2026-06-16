"""FastAPI HTTP bridge — exposes POST /diagnose and GET /healthz.

This server is the sole entry point for the Go agent to request a diagnosis.
It accepts an Incident JSON body, validates it with Pydantic, runs the
DiagnosisService, and returns a Diagnosis JSON. It never executes remediation.

TODO (future prompt — orchestrator): the Go agent will call this endpoint from
the orchestration loop after the correlator emits a new incident.
"""

from __future__ import annotations

import logging
from datetime import datetime
from typing import Optional

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field, field_validator

from diagnoser.contracts import (
    Diagnosis as DiagnosisContract,
    Incident as IncidentContract,
    Signal as SignalContract,
)
from diagnoser.core import DiagnosisService

logger = logging.getLogger(__name__)

app = FastAPI(
    title="AutoSRE Diagnoser",
    description="Advisory-only diagnosis service. Returns Diagnosis; never executes remediation.",
    version="0.4.0",
)

# Module-level service instance; tests can replace it via app.state.
_service: DiagnosisService | None = None


def get_service() -> DiagnosisService:
    global _service
    if _service is None:
        _service = DiagnosisService()
    return _service


# ---------------------------------------------------------------------------
# Pydantic request / response models (match the Go JSON contract)
# ---------------------------------------------------------------------------


class SignalIn(BaseModel):
    id: str
    source: str
    namespace: str
    resource: str
    severity: str
    kind: str = ""
    reason: str = ""
    message: str = ""
    labels: dict[str, str] = Field(default_factory=dict)
    received_at: Optional[datetime] = None


class IncidentIn(BaseModel):
    id: str
    signals: list[SignalIn]
    affected_resources: list[str]
    severity: str
    opened_at: Optional[datetime] = None
    updated_at: Optional[datetime] = None


class DiagnosisOut(BaseModel):
    incident_id: str
    root_cause: str
    failure_mode: str
    proposed_action: str
    confidence: float
    blast_radius: str
    source: str
    diagnosed_at: datetime

    @field_validator("confidence")
    @classmethod
    def clamp_confidence(cls, v: float) -> float:
        return max(0.0, min(1.0, v))


# ---------------------------------------------------------------------------
# Endpoints
# ---------------------------------------------------------------------------


@app.get("/healthz")
def healthz() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/diagnose", response_model=DiagnosisOut)
def diagnose(body: IncidentIn, request: Request) -> DiagnosisOut:
    """Accept an Incident JSON body and return a validated Diagnosis."""
    # Convert Pydantic model → internal contract type.
    signals = [
        SignalContract(
            id=s.id,
            source=s.source,
            namespace=s.namespace,
            resource=s.resource,
            severity=s.severity,
            kind=s.kind,
            reason=s.reason,
            message=s.message,
            labels=s.labels,
            received_at=s.received_at or datetime.utcnow(),
        )
        for s in body.signals
    ]
    incident = IncidentContract(
        id=body.id,
        signals=signals,
        affected_resources=body.affected_resources,
        severity=body.severity,
        opened_at=body.opened_at or datetime.utcnow(),
        updated_at=body.updated_at or datetime.utcnow(),
    )

    try:
        svc = get_service()
        d: DiagnosisContract = svc.diagnose(incident)
    except Exception as exc:
        logger.error("diagnose: unexpected error: %r", exc)
        raise HTTPException(status_code=500, detail="diagnosis service error") from exc

    return DiagnosisOut(
        incident_id=d.incident_id,
        root_cause=d.root_cause,
        failure_mode=d.failure_mode,
        proposed_action=d.proposed_action,
        confidence=d.confidence,
        blast_radius=d.blast_radius,
        source=d.source,
        diagnosed_at=d.diagnosed_at,
    )


@app.exception_handler(Exception)
async def global_exception_handler(request: Request, exc: Exception) -> JSONResponse:
    logger.error("unhandled exception: %r", exc)
    return JSONResponse(status_code=500, content={"detail": "internal server error"})
