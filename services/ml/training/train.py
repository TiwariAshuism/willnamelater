"""Fit the supervised LightGBM fraud classifier, gated by the data floor."""

from __future__ import annotations

from dataclasses import dataclass

from training.features import to_dataset
from training.gate import meets_floor

SEED = 42
# Fraction of the temporally-ordered data used for training; the newest rows are
# held out for validation, so the model is judged on decisions it did not see —
# the honest analog of scoring future audits, and a guard against overfitting to
# one review era.
TRAIN_FRACTION = 0.8


@dataclass(frozen=True)
class TrainResult:
    """The outcome of a training run. When ``trained`` is false the floor was not
    met and no artifact should be written — ``model_bytes`` is None."""

    trained: bool
    counts: dict
    model_bytes: bytes | None = None
    metrics: dict | None = None


def train(labels, *, seed: int = SEED) -> TrainResult:
    """Fit the classifier, or return ``trained=False`` when below the data floor.

    LightGBM is imported lazily so the pure modules (features, gate) — and the
    tests that exercise them — never require the training extra to be installed.
    """
    features, targets, resolved_at = to_dataset(labels)
    ok, counts = meets_floor(targets)
    if not ok:
        return TrainResult(trained=False, counts=counts)

    order = _temporal_order(resolved_at)
    split = max(1, int(len(order) * TRAIN_FRACTION))
    train_idx, val_idx = order[:split], order[split:]

    import lightgbm as lgb

    dtrain = lgb.Dataset(
        [features[i] for i in train_idx],
        label=[targets[i] for i in train_idx],
    )
    params = {
        "objective": "binary",
        "num_leaves": 15,
        "learning_rate": 0.05,
        "min_data_in_leaf": 5,
        "seed": seed,
        # Determinism: identical inputs must yield byte-identical models, so a
        # re-run is reproducible and a version hash is meaningful.
        "deterministic": True,
        "force_row_wise": True,
        "num_threads": 1,
        "verbose": -1,
    }
    booster = lgb.train(params, dtrain, num_boost_round=50)
    metrics = _evaluate(
        booster, [features[i] for i in val_idx], [targets[i] for i in val_idx]
    )
    return TrainResult(
        trained=True,
        counts=counts,
        model_bytes=booster.model_to_string().encode("utf-8"),
        metrics=metrics,
    )


def _temporal_order(resolved_at):
    """Indices sorted by resolved_at ascending (older first); ties keep input
    order, so the split is stable."""
    return sorted(range(len(resolved_at)), key=lambda i: (resolved_at[i], i))


def _evaluate(booster, features_val, targets_val):
    if not features_val:
        return {}
    preds = booster.predict(features_val)
    predicted = [1 if p >= 0.5 else 0 for p in preds]
    return {
        "positive": _precision_recall(targets_val, predicted, 1),
        "negative": _precision_recall(targets_val, predicted, 0),
        "n_val": len(targets_val),
    }


def _precision_recall(y_true, y_pred, cls):
    """Per-class precision/recall — not accuracy, which is misleading on the
    class-imbalanced label set the dispute queue produces."""
    tp = sum(1 for t, p in zip(y_true, y_pred, strict=True) if t == cls and p == cls)
    fp = sum(1 for t, p in zip(y_true, y_pred, strict=True) if t != cls and p == cls)
    fn = sum(1 for t, p in zip(y_true, y_pred, strict=True) if t == cls and p != cls)
    precision = tp / (tp + fp) if (tp + fp) else 0.0
    recall = tp / (tp + fn) if (tp + fn) else 0.0
    return {"precision": precision, "recall": recall}
