"""GET /v1/ml/drift — operator-facing prediction-drift estimate.

Exposes the lightweight population-stability signal (:mod:`app.serving.drift`)
so an operator can see when recent traffic has shifted enough to warrant an
emergency retrain. Read-only and cheap; every field is an estimate.
"""

from __future__ import annotations

from fastapi import APIRouter

from app.registry import get_registry
from app.schemas import DriftResponse
from app.serving.drift import get_drift_monitor

router = APIRouter(prefix="/v1/ml", tags=["ml"])


@router.get("/drift", response_model=DriftResponse)
def drift() -> DriftResponse:
    registry = get_registry()
    snap = get_drift_monitor().snapshot()
    return DriftResponse(
        status=snap["status"],
        psi=snap["psi"],
        sample_count=snap["sample_count"],
        reference_count=snap["reference_count"],
        current_count=snap["current_count"],
        min_per_window=snap["min_per_window"],
        psi_threshold=snap["psi_threshold"],
        model_version=registry.active_version(),
        challenger_version=registry.shadow_version(),
        detail=snap["detail"],
    )
