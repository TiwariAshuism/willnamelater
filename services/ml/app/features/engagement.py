"""Engagement-quality features.

The expected-engagement curve below is a *published industry benchmark* — the
well-known pattern that engagement rate falls as follower count rises — not
data we trained on. It anchors the "how far from normal is this account's
engagement" signal. Both signals here are bounded to [0, 1].
"""

from __future__ import annotations

import numpy as np

from app.schemas import PostMetrics

# Expected engagement rate (engagements / follower) by follower tier. Declining
# curve, midpoints of commonly cited micro/macro benchmark ranges. Used only as
# a reference anchor for deviation, never presented as the account's own value.
_ENGAGEMENT_CURVE = (
    (10_000, 0.050),
    (100_000, 0.035),
    (500_000, 0.020),
    (1_000_000, 0.015),
)
_ENGAGEMENT_FLOOR = 0.012

# Deviation (in absolute engagement-rate points) that saturates the signal.
_ENGAGEMENT_DEV_SPAN = 0.05

# Likes-per-comment ratio treated as normal before the signal rises, and the
# span over which it saturates. Bought likes with organic-looking comments push
# this ratio far above the typical band.
_LC_NORMAL = 120.0
_LC_SPAN = 600.0


def expected_engagement_rate(follower_count: int) -> float:
    """Benchmark engagement rate for an account of this size."""
    for threshold, rate in _ENGAGEMENT_CURVE:
        if follower_count < threshold:
            return rate
    return _ENGAGEMENT_FLOOR


def observed_engagement_rate(
    posts: list[PostMetrics], follower_count: int
) -> float | None:
    """Mean per-post engagement rate, or ``None`` if it cannot be computed."""
    if not posts or follower_count <= 0:
        return None
    engagements = np.array([p.likes + p.comments for p in posts], dtype=float)
    return float(np.mean(engagements) / follower_count)


def engagement_deviation_signal(
    posts: list[PostMetrics], follower_count: int
) -> float:
    """How far observed engagement sits from the benchmark, in [0, 1].

    Two-sided: both suspiciously low engagement (inflated follower base) and
    suspiciously high engagement (bought likes or pod activity) raise it.
    """
    observed = observed_engagement_rate(posts, follower_count)
    if observed is None:
        return 0.0
    expected = expected_engagement_rate(follower_count)
    deviation = abs(observed - expected)
    return min(deviation / _ENGAGEMENT_DEV_SPAN, 1.0)


def like_comment_ratio_signal(posts: list[PostMetrics]) -> float:
    """Suspicion from an abnormally high likes-to-comments ratio, in [0, 1].

    Uses the median across posts so a single viral post does not dominate.
    Posts with zero comments contribute the post's like count against a floor
    of one comment, which is the realistic worst case for bought engagement.
    """
    if not posts:
        return 0.0
    ratios = np.array(
        [p.likes / max(p.comments, 1) for p in posts], dtype=float
    )
    median_ratio = float(np.median(ratios))
    excess = median_ratio - _LC_NORMAL
    if excess <= 0.0:
        return 0.0
    return min(excess / _LC_SPAN, 1.0)
