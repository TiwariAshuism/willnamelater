"""POST /v1/pods/detect — coordinated-commenter cliques from comment events."""

from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter

from app.models.cliques import detect_cliques
from app.registry import get_registry
from app.schemas import Pod, PodsDetectRequest, PodsDetectResponse, SignalContribution

router = APIRouter(prefix="/v1/pods", tags=["pods"])

# Number of analyzed commenters at which volume no longer limits confidence.
_VOLUME_TARGET = 30.0
_CONFIDENCE_CAP = 0.6
_CONFIDENCE_FLOOR = 0.15

# Cliques-per-commenter at which the density signal reaches half strength. The
# three-orders-of-magnitude separation in the research (thousands of cliques
# over hundreds of nodes vs a couple dozen) sits far above this, so it saturates
# for coordinated channels while staying low for ordinary fandoms.
_DENSITY_HALF = 1.0

# Weights over the two reported coordination signals; sum to 1.0.
_W_CLIQUE_DENSITY = 0.7
_W_MEMBERSHIP = 0.3


def _density_value(clique_count: int, commenters: int) -> float:
    """Cliques-per-commenter mapped to [0, 1], normalized by channel size.

    Dividing by commenter count blunts the large-fandom false positive: a big
    audience produces many cliques but also many nodes, so the ratio stays low
    unless the co-commenting is genuinely dense.
    """
    if commenters <= 0:
        return 0.0
    per_node = clique_count / commenters
    return per_node / (per_node + _DENSITY_HALF)


@router.post("/detect", response_model=PodsDetectResponse)
def detect(request: PodsDetectRequest) -> PodsDetectResponse:
    result = detect_cliques(
        request.events, request.min_pod_size, request.min_shared_posts
    )

    pods = [
        Pod(
            members=r.members,
            size=len(r.members),
            cohesion=round(r.cohesion, 6),
            confidence=round(r.confidence, 6),
        )
        for r in result.pods
    ]

    density = _density_value(result.clique_count, result.commenters_analyzed)
    membership = result.membership_fraction
    signals = [
        SignalContribution(
            name="maximal_clique_density",
            value=round(density, 6),
            weight=_W_CLIQUE_DENSITY,
            weighted=round(density * _W_CLIQUE_DENSITY, 6),
            detail=(
                f"{result.clique_count} maximal cliques of size "
                f">= {request.min_pod_size}, normalized per commenter."
            ),
        ),
        SignalContribution(
            name="clique_membership_fraction",
            value=round(membership, 6),
            weight=_W_MEMBERSHIP,
            weighted=round(membership * _W_MEMBERSHIP, 6),
            detail="Share of commenters sitting inside any coordinated clique.",
        ),
    ]

    volume_factor = min(result.commenters_analyzed / _VOLUME_TARGET, 1.0)
    detection_factor = density * _W_CLIQUE_DENSITY + membership * _W_MEMBERSHIP
    span = _CONFIDENCE_CAP - _CONFIDENCE_FLOOR
    raw = _CONFIDENCE_FLOOR + span * 0.5 * (volume_factor + detection_factor)
    # A reduced (partial) graph is a weaker estimate; never let it read stronger.
    if result.partial:
        raw = min(raw, _CONFIDENCE_FLOOR + 0.5 * span)
    confidence = round(min(raw, _CONFIDENCE_CAP), 4)

    return PodsDetectResponse(
        pods=pods,
        clique_count=result.clique_count,
        clique_membership_fraction=round(membership, 6),
        commenters_analyzed=result.commenters_analyzed,
        partial=result.partial,
        signals=signals,
        confidence=confidence,
        model_version=get_registry().active_version(),
        generated_at=datetime.now(UTC),
    )
