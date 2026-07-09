"""Per-call anomaly detection over a single follower-growth series.

An IsolationForest is fitted on the request's own day-over-day growth features
and never persisted. With no labels and no cross-account history, "anomalous"
can only mean "unlike the other days in this same series" — which is exactly
what a per-call isolation forest measures. A fixed random seed makes it fully
deterministic for a given input.
"""

from __future__ import annotations

import numpy as np
from sklearn.ensemble import IsolationForest

from app.features.follower import FollowerFeatures

# Deterministic fit across identical requests.
_RANDOM_STATE = 42

# Minimum number of deltas before a forest is meaningful. Below this the signal
# is reported as 0 (insufficient data) rather than guessed.
_MIN_SAMPLES = 4

# Logistic steepness mapping the most-isolated point's raw score to [0, 1].
_MAP_STEEPNESS = 4.0


def growth_anomaly_signal(features: FollowerFeatures) -> float:
    """Isolation strength of the single most anomalous day, in [0, 1].

    Each day is described by its follower delta and relative growth rate. The
    forest's ``decision_function`` is negative for outliers, so the negated
    value grows as a day becomes more extreme; the maximum over days is mapped
    through a logistic to [0, 1]. Making one day's gain larger only pushes that
    day further into outlier territory, so the signal does not fall as a spike
    sharpens.
    """
    deltas = features.deltas
    if deltas.size < _MIN_SAMPLES:
        return 0.0

    x = np.column_stack([deltas, features.growth_rates])
    forest = IsolationForest(
        n_estimators=200,
        random_state=_RANDOM_STATE,
        contamination="auto",
    )
    forest.fit(x)

    # decision_function: higher = more normal, negative = outlier.
    outlier_strength = -forest.decision_function(x)
    peak = float(np.max(outlier_strength))
    return 1.0 / (1.0 + np.exp(-_MAP_STEEPNESS * peak))
