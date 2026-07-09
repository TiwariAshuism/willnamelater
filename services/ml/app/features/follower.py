"""Follower-series features and the rule-based signals derived from them.

All signals are bounded to [0, 1]. The growth-spike signal is intentionally
constructed to be *monotone non-decreasing* in the size of the largest daily
gain, because that is a property we can assert in tests despite having no
labels: a sharper spike can only look more suspicious, never less.
"""

from __future__ import annotations

import math
from dataclasses import dataclass

import numpy as np

from app.schemas import FollowerPoint

# Saturation constant for the spike ratio -> signal map. Larger values make the
# curve approach 1.0 more slowly, so a ratio of ~9x maps to ~0.5.
_SPIKE_SCALE = 8.0

# Width (in decades) of the follower/following log-ratio band treated as
# ordinary before the ratio signal starts to rise.
_RATIO_NORMAL_DECADES = 1.0
_RATIO_SPAN_DECADES = 3.0


@dataclass(frozen=True)
class FollowerFeatures:
    """Derived quantities for one follower series and account snapshot."""

    deltas: np.ndarray
    growth_rates: np.ndarray
    follower_count: int
    following_count: int

    @property
    def series_len(self) -> int:
        # Number of raw observations = deltas + 1.
        return int(self.deltas.size) + 1 if self.deltas.size else 0


def extract_follower_features(
    series: list[FollowerPoint],
    follower_count: int,
    following_count: int,
) -> FollowerFeatures:
    """Compute day-over-day deltas and growth rates from the raw series.

    Points are sorted by timestamp first so callers need not pre-order them.
    """
    ordered = sorted(series, key=lambda p: p.timestamp)
    counts = np.array([p.count for p in ordered], dtype=float)
    if counts.size < 2:
        empty = np.empty(0, dtype=float)
        return FollowerFeatures(empty, empty, follower_count, following_count)

    deltas = np.diff(counts)
    # Growth rate relative to the prior level; guards the zero-follower start.
    prior = np.maximum(counts[:-1], 1.0)
    growth_rates = deltas / prior
    return FollowerFeatures(deltas, growth_rates, follower_count, following_count)


def growth_spike_signal(features: FollowerFeatures) -> float:
    """Strength of the sharpest positive follower jump, in [0, 1].

    Defined as a saturating function of the ratio between the largest positive
    daily gain and the *median* positive daily gain. The median is used as the
    baseline precisely because increasing only the largest gain leaves it
    unchanged, which makes the signal provably monotone in that largest gain.
    """
    deltas = features.deltas
    positive = deltas[deltas > 0.0]
    if positive.size < 2:
        return 0.0

    baseline = float(np.median(positive))
    if baseline <= 0.0:
        return 0.0

    ratio = float(np.max(positive)) / baseline
    if ratio <= 1.0:
        return 0.0

    excess = ratio - 1.0
    return excess / (excess + _SPIKE_SCALE)


def follower_following_signal(follower_count: int, following_count: int) -> float:
    """Suspicion from an implausible follower/following balance, in [0, 1].

    Ordinary accounts keep followers and following within roughly a decade of
    each other. A log-ratio far outside that band — a tiny account with a huge
    follower count, or an aggressive mass-follower — is what this flags. The
    signal is two-sided and saturating.
    """
    ratio = math.log10((follower_count + 1.0) / (following_count + 1.0))
    excess = abs(ratio) - _RATIO_NORMAL_DECADES
    if excess <= 0.0:
        return 0.0
    return min(excess / _RATIO_SPAN_DECADES, 1.0)
