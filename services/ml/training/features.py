"""Project the admin label export onto a supervised training matrix."""

from __future__ import annotations

# The frozen feature order. It mirrors the FraudFeatures the audit actually
# recorded (fraud_result, migration 000020) and the order the README fixes. It
# must never be reordered: a persisted model's inputs are positional, so a
# reorder would silently feed the model the wrong columns.
FEATURE_ORDER = (
    "risk_score",
    "engagement_anomaly",
    "clique_count",
    "clique_membership_fraction",
    "confidence",
)

#: Bump this whenever FEATURE_ORDER changes. Positional model inputs mean a stored
#: model is only valid for the exact vector it trained on, and a silent reorder
#: feeds a model the wrong columns.
#:
#: v2 removed two fabricated columns that were never measurements at all:
#:   * ``fake_follower_rate`` was the composite risk score renamed (nothing in the
#:     pipeline has ever seen a follower list), and
#:   * ``bot_comment_rate`` was a bit-for-bit DUPLICATE of
#:     ``clique_membership_fraction`` (no comment text was ever classified), so the
#:     6-column vector carried only 5 distinct values and handed any model trained
#:     on it a perfectly collinear pair presented as independent evidence.
#: Rows captured under v1 are not comparable and must not be trained on.
FEATURE_ORDER_VERSION = 2


def to_dataset(labels):
    """Project training labels onto ``(X, y, resolved_at, influencer_ids)``.

    Only rows the audit could actually feature are kept: ``has_features`` must be
    true AND the stored estimate must be ``present``. Rows without features are
    DROPPED, never zero-filled — an all-zero vector would teach the model that
    "no signal" is a specific point in feature space, which is false. ``y`` is 1
    for a fraudulent label (dispute rejected), 0 for legitimate (upheld).

    ``influencer_ids`` is the GROUP KEY. It is carried (rather than derived later)
    because both the data floor and the train/held-out split are taken over
    CREATORS, not rows: the same creator is re-audited on a schedule, so rows are
    not independent examples. An export without an influencer_id yields blanks,
    which count as zero distinct creators — i.e. below the floor, train nothing.
    That is the correct, honest failure: we cannot prove the rows are independent.
    """
    features: list[list[float]] = []
    targets: list[int] = []
    resolved_at: list[str] = []
    influencer_ids: list[str] = []
    for row in labels:
        if not row.get("has_features"):
            continue
        feats = row.get("features") or {}
        if not feats.get("present"):
            continue
        features.append([float(feats.get(name, 0.0)) for name in FEATURE_ORDER])
        targets.append(1 if row.get("label") else 0)
        resolved_at.append(str(row.get("resolved_at") or ""))
        influencer_ids.append(str(row.get("influencer_id") or ""))
    return features, targets, resolved_at, influencer_ids
