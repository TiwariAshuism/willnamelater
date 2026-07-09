"""Composite fraud estimate: the honest cold-start scorer.

Blends the per-call IsolationForest anomaly signal with four explicit
heuristics (growth spike, follower/following balance, engagement-curve
deviation, like-to-comment ratio) into a single 0-100 risk estimate with a
per-signal breakdown. Every input to the blend is bounded to [0, 1] and the
weights sum to 1, so the composite is bounded by construction.
"""

from __future__ import annotations

from dataclasses import dataclass

from app.features.engagement import (
    engagement_deviation_signal,
    like_comment_ratio_signal,
)
from app.features.follower import (
    extract_follower_features,
    follower_following_signal,
    growth_spike_signal,
)
from app.models.anomaly import growth_anomaly_signal
from app.schemas import FraudScoreRequest, SignalContribution

# Signal weights, summing to 1.0. The two growth-driven signals dominate
# because a manufactured audience shows up first in the follower time series.
_WEIGHTS: dict[str, float] = {
    "growth_spike": 0.28,
    "follower_growth_anomaly": 0.22,
    "follower_following_ratio": 0.18,
    "engagement_deviation": 0.20,
    "like_comment_ratio": 0.12,
}

_SIGNAL_DETAIL: dict[str, str] = {
    "growth_spike": (
        "Sharpest follower gain relative to the account's typical daily gain."
    ),
    "follower_growth_anomaly": (
        "Isolation of the most unusual day in the follower series."
    ),
    "follower_following_ratio": (
        "Deviation of the follower/following balance from a normal band."
    ),
    "engagement_deviation": (
        "Distance of observed engagement from the size-adjusted benchmark."
    ),
    "like_comment_ratio": "Excess of likes per comment over the organic band.",
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
    """Produce the composite fraud estimate for one account."""
    account = request.account
    features = extract_follower_features(
        request.follower_series, account.follower_count, account.following_count
    )

    values: dict[str, float] = {
        "growth_spike": growth_spike_signal(features),
        "follower_growth_anomaly": growth_anomaly_signal(features),
        "follower_following_ratio": follower_following_signal(
            account.follower_count, account.following_count
        ),
        "engagement_deviation": engagement_deviation_signal(
            request.posts, account.follower_count
        ),
        "like_comment_ratio": like_comment_ratio_signal(request.posts),
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
