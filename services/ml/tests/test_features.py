"""Property tests for the pure feature functions and per-account models.

No labeled ground truth is asserted. These check boundedness, determinism, and
the monotonicity property that a sharper follower spike cannot produce a
*weaker* growth-spike signal. The engagement benchmark used below is a clearly
synthetic test fixture (declining round numbers), exercised for its *shape*, not
because any real account is claimed fraudulent.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

from app.features.comments import (
    classify_comment,
    duplicate_norms,
    is_emoji_only,
)
from app.features.engagement import (
    engagement_deviation_signal,
    expected_engagement_rate,
    like_comment_ratio_signal,
)
from app.features.follower import (
    extract_follower_features,
    follower_following_signal,
    growth_spike_signal,
)
from app.models.undbot import undbot_signal
from app.schemas import (
    CommentLabel,
    EngagementBenchmark,
    EngagementBenchmarkPoint,
    PostMetrics,
)

_BASE = datetime(2026, 1, 1, tzinfo=UTC)

_BENCHMARK = EngagementBenchmark(
    curve=[
        EngagementBenchmarkPoint(follower_threshold=10_000, expected_rate=0.05),
        EngagementBenchmarkPoint(follower_threshold=100_000, expected_rate=0.03),
        EngagementBenchmarkPoint(follower_threshold=500_000, expected_rate=0.02),
        EngagementBenchmarkPoint(follower_threshold=1_000_000, expected_rate=0.01),
    ],
    floor=0.005,
    source="test-fixture",
)


def _post(likes: int, comments: int, views: int | None = None) -> PostMetrics:
    return PostMetrics(
        post_id="p1", timestamp=_BASE, likes=likes, comments=comments, views=views
    )


def _series(counts: list[int]):
    from app.schemas import FollowerPoint

    return [
        FollowerPoint(timestamp=_BASE + timedelta(days=i), count=c)
        for i, c in enumerate(counts)
    ]


def _spiked_counts(base_step: int, days: int, spike_day: int, extra: int) -> list[int]:
    """Steady growth with a one-day step of ``extra`` that persists after."""
    counts = [10_000]
    for i in range(1, days):
        step = base_step + (extra if i == spike_day else 0)
        counts.append(counts[-1] + step)
    return counts


def test_growth_spike_signal_is_monotone_in_spike_size() -> None:
    prev = -1.0
    for extra in (0, 2_000, 8_000, 32_000, 128_000):
        counts = _spiked_counts(100, 20, spike_day=10, extra=extra)
        feats = extract_follower_features(_series(counts), counts[-1], 300)
        signal = growth_spike_signal(feats)
        assert signal is not None  # 19 positive deltas: a measurable baseline
        assert 0.0 <= signal <= 1.0
        assert signal >= prev  # sharper spike never lowers the signal
        prev = signal
    assert prev > 0.0  # a large spike must actually register


def test_growth_spike_signal_flat_series_is_measured_zero() -> None:
    # A measured 0.0 is a real OBSERVATION ("we looked, growth is flat"), and must
    # stay distinct from the None the signal now returns when it could not look at
    # all. A present-but-flat series has a baseline, so it is measured, not absent.
    counts = list(range(10_000, 10_000 + 20 * 100, 100))
    feats = extract_follower_features(_series(counts), counts[-1], 300)
    signal = growth_spike_signal(feats)
    assert signal is not None
    assert signal == 0.0


def test_growth_spike_signal_short_series_is_absent_not_zero() -> None:
    # EXPECTATION CHANGED (was test_growth_spike_signal_short_series_is_zero,
    # asserting 0.0). Two follower points give a single delta — not enough to
    # establish a baseline, so the spike is UNMEASURABLE. The old 0.0 was a
    # full-weight vote for "no suspicious growth" cast over growth nobody ever
    # saw; the signal now says None ("we could not look") and score_fraud
    # renormalizes it away instead of counting it as evidence of innocence.
    feats = extract_follower_features(_series([10_000, 10_100]), 10_100, 300)
    assert growth_spike_signal(feats) is None

    # An empty series is likewise absent, never a clean zero.
    assert growth_spike_signal(extract_follower_features([], 10_100, 300)) is None


def test_follower_following_signal_bounds_and_band() -> None:
    # Balanced account: inside the normal band -> 0.
    assert follower_following_signal(5_000, 4_000) == 0.0
    # Tiny following, large follower base: elevated and bounded.
    extreme = follower_following_signal(1_000_000, 5)
    assert 0.0 < extreme <= 1.0


def test_engagement_deviation_bounds_and_is_absent_without_inputs() -> None:
    # EXPECTATION CHANGED (was test_engagement_deviation_bounds_and_requires_
    # benchmark, asserting 0.0 for both unmeasurable cases). An uncomputable
    # deviation is now None — ABSENT — not 0.0. Feeding 0.0 into the weighted
    # composite does not "skip" the signal: it votes "engagement perfectly normal"
    # at full weight for an account whose engagement was never compared to
    # anything, which is how an unexamined account gets certified clean.
    posts = [_post(likes=50, comments=5)]
    signal = engagement_deviation_signal(posts, 100_000, _BENCHMARK)
    assert signal is not None
    assert 0.0 <= signal <= 1.0
    # No posts -> nothing to compare -> absent.
    assert engagement_deviation_signal([], 100_000, _BENCHMARK) is None
    # No sourced benchmark -> nothing to compare AGAINST; never a guessed curve,
    # and never a fabricated "clean" 0.
    assert engagement_deviation_signal(posts, 100_000, None) is None


def test_expected_engagement_curve_declines() -> None:
    rates = [
        expected_engagement_rate(n, _BENCHMARK)
        for n in (5_000, 50_000, 300_000, 2_000_000)
    ]
    assert rates == sorted(rates, reverse=True)


def test_like_comment_ratio_signal_bounds() -> None:
    organic = [_post(likes=200, comments=20)]
    inflated = [_post(likes=50_000, comments=3)]
    # A measured 0.0: posts exist and their ratio sits inside the organic band.
    assert like_comment_ratio_signal(organic) == 0.0
    signal = like_comment_ratio_signal(inflated)
    assert signal is not None
    assert 0.0 < signal <= 1.0


def test_like_comment_ratio_signal_is_absent_without_posts() -> None:
    # An empty feed is not a clean feed: with no posts the ratio is unmeasurable,
    # so the signal is None and must not be counted as evidence of innocence.
    assert like_comment_ratio_signal([]) is None


def test_undbot_signal_is_bounded_and_deterministic() -> None:
    posts = [_post(likes=300, comments=25, views=1_000), _post(likes=10, comments=1)]
    first = undbot_signal(posts, follower_count=200_000, following_count=50)
    second = undbot_signal(posts, follower_count=200_000, following_count=50)
    assert first == second  # pure, no fitted estimator or randomness
    for v in (
        first.value,
        first.posting_type_concentration,
        first.posting_influence_scarcity,
        first.follow_ratio_imbalance,
    ):
        assert 0.0 <= v <= 1.0


def test_undbot_influence_scarcity_rises_as_engagement_vanishes() -> None:
    # An audience that does not engage reads as more scarce (more bot-like).
    engaged = undbot_signal([_post(1_000, 200)], 100_000, 100)
    silent = undbot_signal([_post(1, 0)], 100_000, 100)
    assert silent.posting_influence_scarcity > engaged.posting_influence_scarcity


def test_comment_classification_rules() -> None:
    dupes = duplicate_norms(["Nice post!", "nice post", "a real thoughtful reply here"])
    assert is_emoji_only("🔥🔥🔥")

    label, conf, _ = classify_comment("🔥🔥🔥", dupes)
    assert label is CommentLabel.emoji_only
    assert 0.0 <= conf <= 1.0

    label, _, _ = classify_comment("Nice post!", dupes)
    assert label is CommentLabel.duplicate

    label, _, _ = classify_comment("wow", set())
    assert label is CommentLabel.generic

    label, _, _ = classify_comment(
        "This breakdown of your retention curve is genuinely useful", set()
    )
    assert label is CommentLabel.genuine
