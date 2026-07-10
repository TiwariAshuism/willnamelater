"""UnDBot-inspired per-account tie-breaker.

UnDBot (ACM 10.1145/3660522) is an unsupervised, label-free bot detector that
reduces to three interpretable per-account metrics — posting-type distribution,
posting influence (avg engagement per original post), and the follow-to-follower
ratio ``ff = (following + 1) / (follower + 1)`` — and then runs *structural
entropy over a multi-relational graph built across many accounts*.

We can compute the three metrics for a single account, but we deliberately do
**not** claim to run UnDBot: the discriminative power lives in the cross-account
structural-entropy step, and one audit is one account. So this is weighted as a
tie-breaker, never the headline, and the output says so. The headline
coordination signal is the co-commenter clique count (:mod:`app.models.cliques`).

Two honest limits are baked in:

* We have no native post-type labels, only whether a post carries video views,
  so the posting-type metric is a two-category proxy — flagged as degraded.
* The follow-to-follower metric reuses :func:`follower_following_signal` so the
  follower/following relationship has a single implementation. UnDBot's raw
  ``ff`` is a monotone transform of that function's log-ratio input; aligning on
  the existing bounded mapping avoids a second, redundant formulation.

The aggregate and every sub-metric are pure functions, bounded to [0, 1], and
fully deterministic — no fitted estimator, no randomness.
"""

from __future__ import annotations

from dataclasses import dataclass

from app.features.follower import follower_following_signal
from app.schemas import PostMetrics

# Weights over the three sub-metrics. Posting influence and the follow ratio are
# the load-bearing ones; the posting-type proxy is degraded, so it is weighted
# least. They sum to 1.0.
_W_POSTING_TYPE = 0.2
_W_POSTING_INFLUENCE = 0.4
_W_FOLLOW_RATIO = 0.4

# Engagement-per-follower at or below which posting influence reads as fully
# scarce (bot-like: an audience that does not engage). A plain, benchmark-free
# scale, intentionally distinct from the sourced engagement-deviation signal.
_INFLUENCE_SCARCITY_SCALE = 0.005


@dataclass(frozen=True)
class UnDBotResult:
    """The aggregate tie-breaker plus its three interpretable sub-metrics."""

    value: float
    posting_type_concentration: float
    posting_influence_scarcity: float
    follow_ratio_imbalance: float


def _posting_type_concentration(posts: list[PostMetrics]) -> float:
    """Concentration of the video / non-video posting split, in [0, 1].

    A degraded proxy for UnDBot's posting-type distribution: our schema exposes
    only whether a post carries video views, so at most two categories exist.
    A perfectly monotonous poster (all one category) scores 1.0, a balanced mix
    scores 0.0. It cannot see reshare/reply/quote behaviour, so it is weighted
    least of the three metrics.
    """
    if not posts:
        return 0.0
    video = sum(1 for p in posts if p.views is not None)
    share = video / len(posts)
    # Distance from an even split, rescaled so 50/50 -> 0 and all-one -> 1.
    return abs(2.0 * share - 1.0)


def _posting_influence_scarcity(
    posts: list[PostMetrics], follower_count: int
) -> float:
    """Scarcity of engagement per follower, in [0, 1].

    UnDBot's posting influence is average engagement per original post; an
    account with a large follower base but near-zero engagement per follower is
    the bot-like case this captures. One-sided (only scarcity is suspicious) and
    benchmark-free, so it stays computable even when no engagement benchmark is
    supplied.
    """
    if not posts or follower_count <= 0:
        return 0.0
    mean_engagement = sum(p.likes + p.comments for p in posts) / len(posts)
    influence_per_follower = mean_engagement / follower_count
    # Low influence -> high scarcity; saturating so it stays in [0, 1].
    return _INFLUENCE_SCARCITY_SCALE / (
        _INFLUENCE_SCARCITY_SCALE + influence_per_follower
    )


def undbot_signal(
    posts: list[PostMetrics], follower_count: int, following_count: int
) -> UnDBotResult:
    """Aggregate the three UnDBot metrics into one bounded tie-breaker."""
    posting_type = _posting_type_concentration(posts)
    influence = _posting_influence_scarcity(posts, follower_count)
    follow_ratio = follower_following_signal(follower_count, following_count)

    value = (
        _W_POSTING_TYPE * posting_type
        + _W_POSTING_INFLUENCE * influence
        + _W_FOLLOW_RATIO * follow_ratio
    )
    return UnDBotResult(
        value=value,
        posting_type_concentration=posting_type,
        posting_influence_scarcity=influence,
        follow_ratio_imbalance=follow_ratio,
    )
