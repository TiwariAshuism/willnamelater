"""Train champion–challenger challengers: a fraud ensemble and a reach regressor.

Contract ``RETRAINING_ARCHITECTURE.md`` §1 / §33:

- The FRAUD challenger is a small **ensemble** of 3 LightGBM classifiers, each
  fit on a different deterministic bootstrap resample of the training slice. At
  score time it reports the **mean** probability AND the **cross-ensemble
  variance** — a second confidence signal: high variance means the members
  disagree, so the point is near a decision boundary the training data did not
  pin down.
- The REACH / valuation challenger is LightGBM **quantile regression** at P10 /
  P50 / P90, yielding a calibrated range rather than a false point estimate.

Everything is deterministic: a fixed seed makes the bootstrap resamples, the
LightGBM training, and therefore the serialized model bytes byte-identical across
runs, so a re-run is reproducible and the ``lgbm-<sha256>`` version pins the
exact model. ``lightgbm`` is imported lazily so the pure modules never require
the training extra.
"""

from __future__ import annotations

import json
import math
import random
from dataclasses import dataclass

from training.features import FEATURE_ORDER

SEED = 42

# Ensemble size for the fraud challenger (contract §1). Small on purpose: enough
# members to expose disagreement (variance) without turning a cheap tabular
# model into a slow one.
ENSEMBLE_SIZE = 3

# Quantiles for the reach regressor: a P10–P90 band around a P50 median.
REACH_QUANTILES = (0.1, 0.5, 0.9)

FRAUD_KIND = "lgbm-fraud-ensemble"
REACH_KIND = "lgbm-reach-quantile"

_FRAUD_PARAMS = {
    "objective": "binary",
    "num_leaves": 15,
    "learning_rate": 0.05,
    "min_data_in_leaf": 5,
    "deterministic": True,
    "force_row_wise": True,
    "num_threads": 1,
    "verbose": -1,
}
_REACH_PARAMS = {
    "objective": "quantile",
    "num_leaves": 15,
    "learning_rate": 0.05,
    "min_data_in_leaf": 5,
    "deterministic": True,
    "force_row_wise": True,
    "num_threads": 1,
    "verbose": -1,
}


@dataclass(frozen=True)
class FraudPrediction:
    """Per-row ensemble output: mean fraud probability and member disagreement."""

    mean: float
    variance: float


@dataclass(frozen=True)
class ReachPrediction:
    """Per-row reach band from the quantile regressor."""

    p10: float
    p50: float
    p90: float


class FraudChallenger:
    """A bagged ensemble of LightGBM fraud classifiers (mean + variance)."""

    def __init__(self, boosters, feature_order=FEATURE_ORDER) -> None:
        self._boosters = list(boosters)
        self.feature_order = tuple(feature_order)

    def predict(self, features) -> list[FraudPrediction]:
        """Mean probability and population variance across ensemble members."""
        if not features:
            return []
        x = _as_matrix(features)
        member_preds = [list(b.predict(x)) for b in self._boosters]
        out: list[FraudPrediction] = []
        for i in range(len(features)):
            vals = [preds[i] for preds in member_preds]
            mean = sum(vals) / len(vals)
            variance = sum((v - mean) ** 2 for v in vals) / len(vals)
            out.append(FraudPrediction(mean=mean, variance=variance))
        return out

    def scores(self, features) -> list[float]:
        """The mean probabilities alone (the served fraud score)."""
        return [p.mean for p in self.predict(features)]

    def to_bytes(self) -> bytes:
        """Deterministic serialization of the whole ensemble."""
        payload = {
            "kind": FRAUD_KIND,
            "feature_order": list(self.feature_order),
            "n_models": len(self._boosters),
            "models": [b.model_to_string() for b in self._boosters],
        }
        return json.dumps(payload, sort_keys=True).encode("utf-8")


class ReachChallenger:
    """P10 / P50 / P90 LightGBM quantile regressors sharing one feature order."""

    def __init__(self, boosters_by_q, feature_order) -> None:
        # boosters_by_q: dict[float, Booster]
        self._boosters_by_q = dict(boosters_by_q)
        self.feature_order = tuple(feature_order)

    def predict(self, features) -> list[ReachPrediction]:
        if not features:
            return []
        x = _as_matrix(features)
        p10 = list(self._boosters_by_q[0.1].predict(x))
        p50 = list(self._boosters_by_q[0.5].predict(x))
        p90 = list(self._boosters_by_q[0.9].predict(x))
        # Quantile crossing is possible with independent regressors; sort each
        # row's triple so P10 <= P50 <= P90 is always an honest band.
        out: list[ReachPrediction] = []
        for lo, mid, hi in zip(p10, p50, p90, strict=True):
            a, b, c = sorted((lo, mid, hi))
            out.append(ReachPrediction(p10=a, p50=b, p90=c))
        return out

    def scores(self, features) -> list[float]:
        """The P50 medians alone (the point reach estimate)."""
        return [p.p50 for p in self.predict(features)]

    def to_bytes(self) -> bytes:
        payload = {
            "kind": REACH_KIND,
            "feature_order": list(self.feature_order),
            "quantiles": {
                str(q): self._boosters_by_q[q].model_to_string()
                for q in REACH_QUANTILES
            },
        }
        return json.dumps(payload, sort_keys=True).encode("utf-8")


def _as_matrix(features):
    """LightGBM 4.x wants a 2-D float array, not a Python list-of-lists."""
    import numpy as np

    return np.asarray(features, dtype=np.float64)


def _bootstrap_indices(n: int, seed: int) -> list[int]:
    """A deterministic bootstrap resample (sample n indices with replacement)."""
    rng = random.Random(seed)
    return [rng.randrange(n) for _ in range(n)]


def train_fraud_challenger(
    features, targets, *, seed: int = SEED, rounds: int = 50
) -> FraudChallenger:
    """Fit the 3-model fraud ensemble on the given (already floor-checked) slice.

    Each member uses a distinct deterministic bootstrap resample so the members
    genuinely differ (otherwise the variance signal would be zero). The data
    floor is the caller's responsibility (gate G0) — this function only trains.
    """
    import lightgbm as lgb

    x = _as_matrix(features)
    n = len(features)
    boosters = []
    for i in range(ENSEMBLE_SIZE):
        idx = _bootstrap_indices(n, seed + i)
        dtrain = lgb.Dataset(x[idx], label=[targets[j] for j in idx])
        params = {**_FRAUD_PARAMS, "seed": seed + i}
        boosters.append(lgb.train(params, dtrain, num_boost_round=rounds))
    return FraudChallenger(boosters, FEATURE_ORDER)


def train_reach_challenger(
    features, targets, feature_order, *, seed: int = SEED, rounds: int = 100
) -> ReachChallenger:
    """Fit the P10 / P50 / P90 reach quantile regressors on the given slice.

    **All three heads train with pinball loss on the SAME SINGLE label: the
    account's median reach.** The band they produce is therefore CROSS-ACCOUNT
    PREDICTIVE UNCERTAINTY — "given what we can see about an account like this,
    the reach we expect lands in [p10, p90]". That is the only quantity a brand
    can act on, because a brand is deciding about an account it has not booked.

    It is tempting to instead train each head on that account's OWN observed
    p10 / p50 / p90 across its posts. **Do not.** Those are two different
    quantities, and swapping them is silent:

    - Per-account quantiles measure WITHIN-ACCOUNT POST SPREAD (how much this
      creator's posts vary), which is systematically NARROWER than the
      uncertainty of a prediction about an unseen account.
    - The swap makes the interval look tight and confident. G1's coverage check
      cannot catch it: the model is then well calibrated for the wrong question,
      so coverage lands inside the band and the gate goes green while the
      shipped interval understates the real risk to the buyer.

    Within-account spread may be persisted and reported SEPARATELY as a
    MEASUREMENT DISCLOSURE ("this creator's own posts ranged X-Y") — it is
    carried on ``ReachDataset.within_account_spread`` and surfaced in the
    validation report under ``measurement_disclosure``. It must NEVER be
    presented as the model's predictive interval.

    ``targets`` must therefore be a flat sequence of scalars. A per-row triple
    is rejected rather than quietly reshaped.
    """
    import lightgbm as lgb

    labels = _scalar_labels(targets)
    dtrain = lgb.Dataset(_as_matrix(features), label=labels)
    boosters_by_q = {}
    for q in REACH_QUANTILES:
        params = {**_REACH_PARAMS, "alpha": q, "seed": seed}
        boosters_by_q[q] = lgb.train(params, dtrain, num_boost_round=rounds)
    return ReachChallenger(boosters_by_q, feature_order)


def _scalar_labels(targets) -> list[float]:
    """Reject any attempt to hand the quantile heads per-row (p10, p50, p90).

    This is a structural guard, not a type nicety: a caller that passes triples
    here would train the heads on within-account spread and ship it as the
    model's predictive interval, and no gate downstream can tell the difference.
    """
    labels: list[float] = []
    for i, t in enumerate(targets):
        if isinstance(t, (list, tuple, dict, set)):
            raise TypeError(
                "reach targets must be the SINGLE median reach label per row; got a "
                f"sequence at index {i}. Training the P10/P90 heads on per-account "
                "quantiles swaps cross-account predictive uncertainty for "
                "within-account post spread — see train_reach_challenger.__doc__."
            )
        labels.append(float(t))
    return labels


def reach_bands(preds: list[ReachPrediction]):
    """Split reach predictions into parallel p10/p50/p90 lists (for validation)."""
    return (
        [p.p10 for p in preds],
        [p.p50 for p in preds],
        [p.p90 for p in preds],
    )


def load_fraud_challenger(model_bytes: bytes) -> FraudChallenger:
    """Reconstruct a fraud ensemble from its serialized bytes (for re-scoring a
    champion during validation, or a shadow model)."""
    import lightgbm as lgb

    payload = json.loads(model_bytes.decode("utf-8"))
    boosters = [lgb.Booster(model_str=m) for m in payload["models"]]
    return FraudChallenger(boosters, tuple(payload["feature_order"]))


def load_reach_challenger(model_bytes: bytes) -> ReachChallenger:
    import lightgbm as lgb

    payload = json.loads(model_bytes.decode("utf-8"))
    boosters_by_q = {
        float(q): lgb.Booster(model_str=m) for q, m in payload["quantiles"].items()
    }
    return ReachChallenger(boosters_by_q, tuple(payload["feature_order"]))


def is_finite(value: float) -> bool:
    return not (math.isnan(value) or math.isinf(value))
