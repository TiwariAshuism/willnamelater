"""Project the admin label export onto a supervised training matrix."""

from __future__ import annotations

# The frozen feature order. It mirrors the FraudFeatures the audit actually
# recorded (fraud_result, migration 000020) and the order the README fixes. It
# must never be reordered: a persisted model's inputs are positional, so a
# reorder would silently feed the model the wrong columns.
FEATURE_ORDER = (
    "fake_follower_rate",
    "bot_comment_rate",
    "engagement_anomaly",
    "clique_count",
    "clique_membership_fraction",
    "confidence",
)


def to_dataset(labels):
    """Project training labels onto ``(X, y, resolved_at)``.

    Only rows the audit could actually feature are kept: ``has_features`` must be
    true AND the stored estimate must be ``present``. Rows without features are
    DROPPED, never zero-filled — an all-zero vector would teach the model that
    "no signal" is a specific point in feature space, which is false. ``y`` is 1
    for a fraudulent label (dispute rejected), 0 for legitimate (upheld).
    """
    features: list[list[float]] = []
    targets: list[int] = []
    resolved_at: list[str] = []
    for row in labels:
        if not row.get("has_features"):
            continue
        feats = row.get("features") or {}
        if not feats.get("present"):
            continue
        features.append([float(feats.get(name, 0.0)) for name in FEATURE_ORDER])
        targets.append(1 if row.get("label") else 0)
        resolved_at.append(str(row.get("resolved_at") or ""))
    return features, targets, resolved_at
