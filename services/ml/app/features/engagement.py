"""Engagement-quality features.

The expected-engagement curve used by :func:`engagement_deviation_signal` is
**not** owned by this service. It is supplied per request by the caller (the Go
``scoring`` module, reading the versioned ``benchmark`` table) as an
:class:`~app.schemas.EngagementBenchmark` carrying its own provenance label.

This is deliberate. The previous hardcoded curve was corroborated only by a
competitor's marketing blog across 24 researched sources (see
``product/research/fraud-detection-signals.md`` §8) yet fed a customer-facing
score. Rather than keep uncited constants as the default source of truth, the
deviation signal simply contributes nothing when no sourced benchmark is
provided. All signals here remain bounded to [0, 1].
"""

from __future__ import annotations

import numpy as np

from app.schemas import EngagementBenchmark, PostMetrics

# Deviation (in absolute engagement-rate points) that saturates the signal.
_ENGAGEMENT_DEV_SPAN = 0.05

# Likes-per-comment ratio treated as normal before the signal rises, and the
# span over which it saturates. Bought likes with organic-looking comments push
# this ratio far above the typical band.
_LC_NORMAL = 120.0
_LC_SPAN = 600.0


def expected_engagement_rate(
    follower_count: int, benchmark: EngagementBenchmark
) -> float:
    """Expected engagement rate for this account size, per the sourced curve.

    The curve knots are read lowest-threshold first; the first threshold the
    account falls under gives its expected rate, else the curve's floor.
    """
    for point in sorted(benchmark.curve, key=lambda p: p.follower_threshold):
        if follower_count < point.follower_threshold:
            return point.expected_rate
    return benchmark.floor


def observed_engagement_rate(
    posts: list[PostMetrics], follower_count: int
) -> float | None:
    """Mean per-post engagement rate, or ``None`` if it cannot be computed."""
    if not posts or follower_count <= 0:
        return None
    engagements = np.array([p.likes + p.comments for p in posts], dtype=float)
    return float(np.mean(engagements) / follower_count)


def engagement_deviation_signal(
    posts: list[PostMetrics],
    follower_count: int,
    benchmark: EngagementBenchmark | None,
) -> float:
    """How far observed engagement sits from the sourced benchmark, in [0, 1].

    Two-sided: both suspiciously low engagement (inflated follower base) and
    suspiciously high engagement (bought likes or pod activity) raise it.
    Returns 0.0 when no benchmark is supplied — the signal is *skipped*, never
    anchored to a fabricated curve.
    """
    if benchmark is None:
        return 0.0
    observed = observed_engagement_rate(posts, follower_count)
    if observed is None:
        return 0.0
    expected = expected_engagement_rate(follower_count, benchmark)
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
