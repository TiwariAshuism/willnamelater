"""POST /v1/comments/classify — RULE-BASED comment-quality buckets.

Three rules govern this endpoint, all learned the hard way:

1. **Its own version namespace.** ``model_version`` comes from the *comment*
   registry (:func:`app.registry.get_comment_registry`), never the fraud one.
   Stamping the fraud registry's version here meant that the day a fraud champion
   was promoted, this 18-phrase regex would start reporting a trained LightGBM
   model's version into a paying customer's PDF. Until a real comment artifact
   exists the version is the hardcoded ``heuristic-comments-v1``.

2. **No rate without a denominator, and no rate at all below n=50.** See
   :func:`app.features.comments.summarize`. Below the floor the response carries
   raw counts and ``sufficient_sample: false``, and ``low_quality_ratio`` is
   ``null`` — not 0.0.

3. **This output is quarantined from the fraud score.** It is returned under
   ``rate_key`` = ``generic_comment_rate_v1`` and must not be blended into the
   0-100 composite until its weight is fitted against real fraud outcomes. A high
   generic-comment rate is not fraud; fan and meme accounts are full of genuine
   emoji.
"""

from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter

from app.features.comments import (
    GENERIC_COMMENT_RATE_KEY,
    MIN_RATE_SAMPLE,
    classify_comment,
    duplicate_norms,
    summarize,
)
from app.registry import get_comment_registry
from app.schemas import (
    CommentClassification,
    CommentLabel,
    CommentsClassifyRequest,
    CommentsClassifyResponse,
)

router = APIRouter(prefix="/v1/comments", tags=["comments"])

# Batch size at which volume no longer limits confidence.
_VOLUME_TARGET = 20.0
_CONFIDENCE_CAP = 0.6
_CONFIDENCE_FLOOR = 0.15


@router.post("/classify", response_model=CommentsClassifyResponse)
def classify(request: CommentsClassifyRequest) -> CommentsClassifyResponse:
    comments = request.comments
    dupes = duplicate_norms([c.text for c in comments])

    classifications: list[CommentClassification] = []
    item_confidences: list[float] = []
    labels: list[CommentLabel] = []
    for item in comments:
        label, item_conf, signals = classify_comment(item.text, dupes)
        labels.append(label)
        item_confidences.append(item_conf)
        classifications.append(
            CommentClassification(
                id=item.id,
                label=label,
                confidence=round(item_conf, 6),
                signals=signals,
            )
        )

    summary = summarize(labels, posts_sampled=request.posts_sampled)

    total = len(comments)
    if total:
        volume_factor = min(total / _VOLUME_TARGET, 1.0)
        mean_item = sum(item_confidences) / total
        confidence = min(
            _CONFIDENCE_FLOOR
            + (_CONFIDENCE_CAP - _CONFIDENCE_FLOOR) * 0.5 * (volume_factor + mean_item),
            _CONFIDENCE_CAP,
        )
    else:
        confidence = _CONFIDENCE_FLOOR

    return CommentsClassifyResponse(
        classifications=classifications,
        low_quality_ratio=summary.low_quality_ratio,
        analyzed_count=summary.analyzed,
        counts=summary.counts,
        low_quality_count=summary.low_quality_count,
        sufficient_sample=summary.sufficient_sample,
        min_sample=MIN_RATE_SAMPLE,
        detail=summary.detail,
        rate_key=GENERIC_COMMENT_RATE_KEY,
        confidence=round(confidence, 4),
        # The COMMENT registry — not the fraud registry. This string must not move
        # when a fraud champion is promoted.
        model_version=get_comment_registry().active_version(),
        generated_at=datetime.now(UTC),
    )
