import pytest

from training.train import train

# These tests actually fit a model, so they need the training extra. When it is
# not installed they skip cleanly — the pure gate/feature/artifact tests still
# cover the pipeline's contracts.
pytest.importorskip("lightgbm")


def _dataset():
    """A synthetic, cleanly-separable label set above the floor (60/60). The
    values are fixtures that exercise the pipeline's mechanics — they are not a
    claim that any specific account is fraudulent."""
    labels = []
    for i in range(60):
        day = (i % 28) + 1
        labels.append(
            {
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
                "resolved_at": f"2026-01-{day:02d}T00:00:00Z",
            }
        )
        labels.append(
            {
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
                "resolved_at": f"2026-02-{day:02d}T00:00:00Z",
            }
        )
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
