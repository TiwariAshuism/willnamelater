"""Composite per-account fraud estimate: the honest cold-start scorer.

This scorer produces the **per-account** half of the fraud picture. Following
the coordination-first pivot (research §2.1), the headline fraud signal is the
co-commenter clique count served by ``/v1/pods/detect``; the per-account signals
here are tie-breakers and explanations, combined with coordination by the Go
``scoring`` module. That is why the UnDBot-style signal is weighted modestly and
labelled a tie-breaker rather than a verdict.

Blends four bounded signals into a single 0-100 estimate with a per-signal
breakdown:

* ``growth_spike`` — sharpest follower jump vs the account's typical daily gain.
* ``engagement_deviation`` — observed engagement vs a *caller-supplied, sourced*
  benchmark (0 when none is supplied; never anchored to a guessed curve).
* ``like_comment_ratio`` — excess likes per comment over the organic band.
* ``coordination_undbot`` — UnDBot's three per-account metrics as a tie-breaker.

Every input is bounded to [0, 1] and the weights sum to 1, so the composite is
bounded by construction. The UnDBot signal already carries the follow/follower
relationship (via the shared :func:`follower_following_signal`), so there is no
separate follower/following signal — that would double-count it.
"""

from __future__ import annotations

from dataclasses import dataclass

from app.features.engagement import (
    engagement_deviation_signal,
    like_comment_ratio_signal,
)
from app.features.follower import (
    extract_follower_features,
    growth_spike_signal,
)
from app.models.undbot import undbot_signal
from app.schemas import FraudScoreRequest, SignalContribution

# Signal weights, summing to 1.0. Growth spike and engagement carry the most
# weight; the UnDBot metrics are a tie-breaker (a single audit cannot run the
# cross-account structural-entropy step UnDBot's power comes from).
_WEIGHTS: dict[str, float] = {
    "growth_spike": 0.35,
    "engagement_deviation": 0.30,
    "like_comment_ratio": 0.15,
    "coordination_undbot": 0.20,
}

_SIGNAL_DETAIL: dict[str, str] = {
    "growth_spike": (
        "Sharpest follower gain relative to the account's typical daily gain."
    ),
    "engagement_deviation": (
        "Distance of observed engagement from the caller-supplied sourced "
        "benchmark; 0 when no benchmark is provided."
    ),
    "like_comment_ratio": "Excess of likes per comment over the organic band.",
    "coordination_undbot": (
        "UnDBot per-account tie-breaker (posting-type, posting-influence, "
        "follow-ratio). Weighted low: structural entropy needs a multi-account "
        "graph a single audit does not have."
    ),
}

# A signal at or above this strength is surfaced as a named flag.
_FLAG_THRESHOLD = 0.6

# Cold-start confidence ceiling: unsupervised output is never fully trusted.
_CONFIDENCE_CAP = 0.65
_CONFIDENCE_FLOOR = 0.15

# Data volumes at which each confidence input is considered fully informative.
_SERIES_TARGET = 14.0
_POSTS_TARGET = 12.0


@dataclass(frozen=True)
class FraudResult:
    score: float
    confidence: float
    signals: list[SignalContribution]
    flags: list[str]


def _confidence(series_len: int, post_count: int) -> float:
    """Confidence from data sufficiency, floored and capped for cold start."""
    series_factor = min(series_len / _SERIES_TARGET, 1.0)
    posts_factor = min(post_count / _POSTS_TARGET, 1.0)
    data = 0.5 * series_factor + 0.5 * posts_factor
    return round(_CONFIDENCE_FLOOR + (_CONFIDENCE_CAP - _CONFIDENCE_FLOOR) * data, 4)


def score_fraud(request: FraudScoreRequest) -> FraudResult:
    """Produce the composite per-account fraud estimate for one account."""
    account = request.account
    features = extract_follower_features(
        request.follower_series, account.follower_count, account.following_count
    )
    undbot = undbot_signal(
        request.posts, account.follower_count, account.following_count
    )

    values: dict[str, float] = {
        "growth_spike": growth_spike_signal(features),
        "engagement_deviation": engagement_deviation_signal(
            request.posts, account.follower_count, request.engagement_benchmark
        ),
        "like_comment_ratio": like_comment_ratio_signal(request.posts),
        "coordination_undbot": undbot.value,
    }

    signals: list[SignalContribution] = []
    composite = 0.0
    for name, weight in _WEIGHTS.items():
        value = values[name]
        weighted = value * weight
        composite += weighted
        signals.append(
            SignalContribution(
                name=name,
                value=round(value, 6),
                weight=weight,
                weighted=round(weighted, 6),
                detail=_SIGNAL_DETAIL[name],
            )
        )

    flags = [name for name, value in values.items() if value >= _FLAG_THRESHOLD]
    score = round(min(composite, 1.0) * 100.0, 4)
    confidence = _confidence(features.series_len, len(request.posts))
    return FraudResult(
        score=score, confidence=confidence, signals=signals, flags=flags
    )
