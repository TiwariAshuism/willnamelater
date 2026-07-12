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


def grouped_temporal_split(captured_at, influencer_ids, fraction=TRAIN_FRACTION):
    """Split into (train, held_out) by time, GROUPED BY INFLUENCER.

    The group key is the whole point. Rows are keyed by ``audit_job_id`` and the
    same creator is re-audited on a schedule, so a plain row-wise temporal split
    puts THE SAME INFLUENCER on both sides: the model memorizes creators it has
    already seen, the held-out gate then measures recall of a memory rather than
    generalization, and it reports a beautiful, meaningless number. No influencer
    may appear on both sides of this split — enforced structurally here by
    splitting GROUPS, not rows.

    Temporal ordering is preserved at the group level: groups are ordered by the
    LAST time we saw them, oldest first, and the newest groups are held out. The
    held-out slice is therefore made of creators the model has never seen — the
    honest analogue of scoring an account a brand has not booked before.

    A row with a blank influencer_id is its own group: an id we do not have
    cannot be proven to be the same creator as any other, and collapsing all the
    blanks into one group would be an identity claim we cannot make.
    """
    groups: dict[str, list[int]] = {}
    for i, raw in enumerate(influencer_ids):
        key = str(raw or "").strip() or f"__unidentified__{i}"
        groups.setdefault(key, []).append(i)

    # Order groups by their most recent capture (then by key, for determinism).
    ordered = sorted(
        groups.items(),
        key=lambda kv: (max(captured_at[i] for i in kv[1]), kv[0]),
    )
    target = len(captured_at) * fraction
    train_idx: list[int] = []
    held_out: list[int] = []
    for pos, (_, idx) in enumerate(ordered):
        last_group = pos == len(ordered) - 1
        # Always leave the newest group on the held-out side; never leave the
        # train side empty.
        if train_idx and (len(train_idx) >= target or last_group):
            held_out.extend(idx)
        else:
            train_idx.extend(idx)
    return sorted(train_idx), sorted(held_out)


def train(labels, *, seed: int = SEED) -> TrainResult:
    """Fit the classifier, or return ``trained=False`` when below the data floor.

    LightGBM is imported lazily so the pure modules (features, gate) — and the
    tests that exercise them — never require the training extra to be installed.
    """
    features, targets, resolved_at, influencer_ids = to_dataset(labels)
    ok, counts = meets_floor(targets, influencer_ids)
    if not ok:
        return TrainResult(trained=False, counts=counts)

    train_idx, val_idx = grouped_temporal_split(resolved_at, influencer_ids)
    if not val_idx:
        # Nothing to validate on without leaking a creator into both sides.
        return TrainResult(trained=False, counts=counts)

    import lightgbm as lgb
    import numpy as np

    dtrain = lgb.Dataset(
        np.asarray([features[i] for i in train_idx], dtype=np.float64),
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
        booster,
        np.asarray([features[i] for i in val_idx], dtype=np.float64),
        [targets[i] for i in val_idx],
    )
    return TrainResult(
        trained=True,
        counts=counts,
        model_bytes=booster.model_to_string().encode("utf-8"),
        metrics=metrics,
    )


def _evaluate(booster, features_val, targets_val):
    if len(features_val) == 0:
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
