"""Honest reporting of a rule-based classifier: denominators, sample floor, firewall.

Three guarantees are pinned here:

* a rate is NEVER printed below ``MIN_RATE_SAMPLE`` comments — the field is
  ``null`` (absent), not 0.0 (a measurement), and raw counts are returned instead;
* every rate that IS printed carries its denominator, and the denominator matches
  the number of comments actually classified (no extrapolation to the account);
* the classifier's output stays OUT of the fraud feature vector.
"""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from app.features.comments import (
    GENERIC_COMMENT_RATE_KEY,
    MIN_RATE_SAMPLE,
    summarize,
)
from app.main import create_app
from app.models.heuristics import FraudResult
from app.schemas import CommentLabel
from training.features import FEATURE_ORDER


@pytest.fixture(scope="module")
def client() -> TestClient:
    return TestClient(create_app())


def _batch(n_low: int, n_genuine: int) -> dict:
    """``n_low`` emoji-only comments (distinct, so they bucket as ``emoji_only``
    rather than collapsing into ``duplicate``) plus ``n_genuine`` real ones."""
    comments = [
        {"id": f"e{i}", "text": "🔥" * (i + 1)} for i in range(n_low)
    ] + [
        {"id": f"r{i}", "text": f"I tried this recipe on Sunday and the dough {i}"}
        for i in range(n_genuine)
    ]
    return {"comments": comments}


# ---------------------------------------------------------------------------
# Sample floor: no rate below n=50
# ---------------------------------------------------------------------------


def test_rate_is_suppressed_below_the_sample_floor(client: TestClient) -> None:
    body = client.post("/v1/comments/classify", json=_batch(3, 6)).json()

    assert body["analyzed_count"] == 9
    assert body["sufficient_sample"] is False
    # NULL, not 0.0 — we did not measure a rate, we declined to.
    assert body["low_quality_ratio"] is None
    assert body["min_sample"] == MIN_RATE_SAMPLE == 50
    # The raw counts are still there, so the caller can say something true.
    assert body["low_quality_count"] == 3
    assert body["counts"]["emoji_only"] == 3
    assert body["counts"]["genuine"] == 6
    assert "insufficient sample" in body["detail"].lower()
    assert "%" not in body["detail"]


def test_empty_batch_reports_no_rate_rather_than_zero(client: TestClient) -> None:
    body = client.post("/v1/comments/classify", json={"comments": []}).json()
    assert body["analyzed_count"] == 0
    assert body["low_quality_ratio"] is None  # never 0.0
    assert body["sufficient_sample"] is False
    assert body["low_quality_count"] == 0


def test_one_comment_below_floor_still_suppresses(client: TestClient) -> None:
    body = client.post("/v1/comments/classify", json=_batch(0, MIN_RATE_SAMPLE - 1))
    payload = body.json()
    assert payload["analyzed_count"] == MIN_RATE_SAMPLE - 1
    assert payload["sufficient_sample"] is False
    assert payload["low_quality_ratio"] is None


# ---------------------------------------------------------------------------
# At or above the floor: a rate, and always with its denominator
# ---------------------------------------------------------------------------


def test_rate_appears_exactly_at_the_floor_with_its_denominator(
    client: TestClient,
) -> None:
    body = client.post("/v1/comments/classify", json=_batch(20, 30)).json()

    assert body["analyzed_count"] == MIN_RATE_SAMPLE == 50
    assert body["sufficient_sample"] is True
    assert body["low_quality_ratio"] == pytest.approx(20 / 50)
    assert body["low_quality_count"] == 20
    # The denominator is printed, and it is the number of comments we classified.
    assert "50 comments" in body["detail"]
    assert "20/50" in body["detail"]
    # And the copy refuses the fraud inference outright.
    assert "not evidence of fraud" in body["detail"]


def test_detail_names_the_post_scope_when_given(client: TestClient) -> None:
    payload = _batch(20, 30) | {"posts_sampled": 6}
    body = client.post("/v1/comments/classify", json=payload).json()
    assert "50 comments sampled from 6 recent posts" in body["detail"]


def test_rate_denominator_is_the_batch_not_the_account(client: TestClient) -> None:
    """The rate is over the comments we were handed. Nothing is extrapolated."""
    body = client.post("/v1/comments/classify", json=_batch(30, 30)).json()
    assert body["analyzed_count"] == len(body["classifications"]) == 60
    assert body["low_quality_ratio"] == pytest.approx(
        body["low_quality_count"] / body["analyzed_count"]
    )


def test_counts_are_always_returned_and_sum_to_the_denominator(
    client: TestClient,
) -> None:
    for generic, genuine in ((2, 3), (25, 35)):
        resp = client.post("/v1/comments/classify", json=_batch(generic, genuine))
        body = resp.json()
        assert set(body["counts"]) == {label.value for label in CommentLabel}
        assert sum(body["counts"].values()) == body["analyzed_count"]
        assert (
            body["low_quality_count"]
            == body["analyzed_count"] - body["counts"]["genuine"]
        )


def test_summarize_unit_floor_boundary() -> None:
    below = summarize([CommentLabel.generic] * (MIN_RATE_SAMPLE - 1))
    assert below.low_quality_ratio is None
    assert below.sufficient_sample is False
    assert below.low_quality_count == MIN_RATE_SAMPLE - 1

    at = summarize([CommentLabel.generic] * MIN_RATE_SAMPLE)
    assert at.sufficient_sample is True
    assert at.low_quality_ratio == pytest.approx(1.0)

    # A genuinely measured zero is 0.0, not None: with a big enough sample, "we
    # looked and found none" is a real measurement and must be reportable.
    clean = summarize([CommentLabel.genuine] * MIN_RATE_SAMPLE)
    assert clean.sufficient_sample is True
    assert clean.low_quality_ratio == 0.0


# ---------------------------------------------------------------------------
# Firewall: the comment signal stays out of the fraud score
# ---------------------------------------------------------------------------


def test_comment_signal_is_not_a_fraud_feature() -> None:
    """Its weight has never been fitted against real fraud outcomes."""
    assert GENERIC_COMMENT_RATE_KEY == "generic_comment_rate_v1"
    assert GENERIC_COMMENT_RATE_KEY not in FEATURE_ORDER
    # The deleted fake must not come back under its old name either.
    assert "bot_comment_rate" not in FEATURE_ORDER
    assert not [name for name in FEATURE_ORDER if "comment" in name]


def test_fraud_vector_never_carries_a_comment_key() -> None:
    from app.features.fraud_vector import build_fraud_vector

    vector = build_fraud_vector(
        FraudResult(score=42.0, confidence=0.5, signals=[], flags=[])
    )
    assert set(vector) == set(FEATURE_ORDER)
    assert GENERIC_COMMENT_RATE_KEY not in vector


def test_fraud_vector_import_guard_rejects_a_leaked_comment_key(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Re-importing the module with a comment key in FEATURE_ORDER must EXPLODE.

    This is the tripwire for the next person who tries to plug the comment
    classifier into the hole ``bot_comment_rate`` left behind.
    """
    import importlib

    import training.features as tf

    monkeypatch.setattr(
        tf, "FEATURE_ORDER", (*FEATURE_ORDER, GENERIC_COMMENT_RATE_KEY), raising=True
    )
    import app.features.fraud_vector as fv

    with pytest.raises(RuntimeError, match="FIREWALL"):
        importlib.reload(fv)

    # Restore the real module state for every test that follows.
    monkeypatch.undo()
    importlib.reload(fv)


def test_response_names_the_quarantined_key(client: TestClient) -> None:
    body = client.post("/v1/comments/classify", json=_batch(2, 2)).json()
    assert body["rate_key"] == GENERIC_COMMENT_RATE_KEY
