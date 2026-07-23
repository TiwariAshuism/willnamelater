import pytest

from training.train import train

# These tests actually fit a model, so they need the training extra. When it is
# not installed they skip cleanly — the pure gate/feature/artifact tests still
# cover the pipeline's contracts.
pytest.importorskip("lightgbm")


def _dataset(*, with_influencer_ids=True):
    """A synthetic, cleanly-separable label set above the floor: 60 positives and
    60 negatives, each from a DISTINCT creator (the floor and the split are taken
    over creators, not rows). The values are fixtures that exercise the pipeline's
    mechanics — they are not a claim that any specific account is fraudulent."""
    labels = []
    for i in range(60):
        month = (i % 4) + 1  # both classes span the same months, so the
        day = (i % 28) + 1   # grouped temporal split is not a class split
        positive = {
            "label": True,
            "has_features": True,
            "features": {
                "present": True,
                "risk_score": 80.0,
                "engagement_anomaly": 0.6,
                "clique_count": 9,
                "clique_membership_fraction": 0.5,
                "confidence": 0.6,
            },
            "resolved_at": f"2026-{month:02d}-{day:02d}T00:00:00Z",
        }
        negative = {
            "label": False,
            "has_features": True,
            "features": {
                "present": True,
                "risk_score": 5.0,
                "engagement_anomaly": 0.05,
                "clique_count": 0,
                "clique_membership_fraction": 0.0,
                "confidence": 0.55,
            },
            "resolved_at": f"2026-{month:02d}-{day:02d}T00:00:00Z",
        }
        if with_influencer_ids:
            positive["influencer_id"] = f"pos-{i}"
            negative["influencer_id"] = f"neg-{i}"
        labels.append(positive)
        labels.append(negative)
    return labels


def test_trains_above_floor_and_is_byte_deterministic():
    labels = _dataset()
    first = train(labels)
    second = train(labels)
    assert first.trained is True
    assert first.counts["positive"] == 60
    assert first.counts["negative"] == 60
    # Same data + seed → byte-identical model, so a re-run is reproducible and the
    # version hash is meaningful.
    assert first.model_bytes == second.model_bytes


def test_metrics_are_per_class_precision_recall():
    result = train(_dataset())
    assert "positive" in result.metrics
    assert "negative" in result.metrics
    assert set(result.metrics["positive"]) == {"precision", "recall"}


def test_refuses_to_train_when_the_rows_cannot_be_attributed_to_creators():
    # Same 120 rows, but the export states no influencer_id. We then cannot prove
    # the rows are independent examples rather than one creator re-audited 120
    # times — so the floor is not met and nothing is trained. "We cannot tell" is
    # a refusal, not a pass.
    result = train(_dataset(with_influencer_ids=False))
    assert result.trained is False
    assert result.model_bytes is None
    assert result.counts["positive_influencers"] == 0
