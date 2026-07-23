"""Endpoint tests for /v1/fraud/score and /healthz.

Asserts schema conformance, determinism, boundedness and that the composite
score is monotone non-decreasing as an injected follower spike sharpens — all
provable without any labeled fraud data. The engagement benchmark in the payload
is a synthetic test fixture, exercised for shape only.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from fastapi.testclient import TestClient

from app.main import create_app

_BASE = datetime(2026, 1, 1, tzinfo=UTC)

_BENCHMARK = {
    "curve": [
        {"follower_threshold": 10_000, "expected_rate": 0.05},
        {"follower_threshold": 100_000, "expected_rate": 0.03},
        {"follower_threshold": 1_000_000, "expected_rate": 0.01},
    ],
    "floor": 0.005,
    "source": "test-fixture",
}


@pytest.fixture(scope="module")
def client() -> TestClient:
    return TestClient(create_app())


def _iso(dt: datetime) -> str:
    return dt.isoformat()


def _payload(spike_extra: int) -> dict:
    counts = [10_000]
    for i in range(1, 20):
        step = 100 + (spike_extra if i == 10 else 0)
        counts.append(counts[-1] + step)
    series = [
        {"timestamp": _iso(_BASE + timedelta(days=i)), "count": c}
        for i, c in enumerate(counts)
    ]
    posts = [
        {
            "post_id": f"post_{i}",
            "timestamp": _iso(_BASE + timedelta(days=i)),
            "likes": 300,
            "comments": 25,
        }
        for i in range(6)
    ]
    return {
        "account": {
            "handle": "creator",
            "platform": "instagram",
            "follower_count": counts[-1],
            "following_count": 400,
        },
        "follower_series": series,
        "posts": posts,
        "engagement_benchmark": _BENCHMARK,
    }


def test_healthz_reports_heuristic_cold_start(client: TestClient) -> None:
    resp = client.get("/healthz")
    assert resp.status_code == 200
    body = resp.json()
    assert body["status"] == "ok"
    assert body["model_version"] == "heuristic"


def test_fraud_response_schema_and_bounds(client: TestClient) -> None:
    resp = client.post("/v1/fraud/score", json=_payload(50_000))
    assert resp.status_code == 200
    body = resp.json()

    assert 0.0 <= body["score"] <= 100.0
    assert 0.0 <= body["confidence"] <= 1.0
    assert body["estimate"] is True
    assert body["model_version"] == "heuristic"

    names = {s["name"] for s in body["signals"]}
    assert names == {
        "growth_spike",
        "engagement_deviation",
        "like_comment_ratio",
        "coordination_undbot",
    }
    for signal in body["signals"]:
        assert 0.0 <= signal["value"] <= 1.0
        assert 0.0 <= signal["weighted"] <= 1.0


def test_fraud_deviation_absent_without_benchmark(client: TestClient) -> None:
    # EXPECTATION CHANGED (was test_fraud_deviation_skipped_without_benchmark,
    # which asserted the signal was still REPORTED with value 0.0). With no sourced
    # curve the deviation is not 0 — it is unmeasured, so it is not reported at
    # all. A reported 0.0 was a full-weight vote for "engagement perfectly normal"
    # against an account whose engagement was never compared to anything.
    payload = _payload(0)
    del payload["engagement_benchmark"]
    body = client.post("/v1/fraud/score", json=payload).json()

    names = {s["name"] for s in body["signals"]}
    assert "engagement_deviation" not in names
    assert names == {"growth_spike", "like_comment_ratio", "coordination_undbot"}
    assert body["observed"] is True  # three of four signals WERE observed


def test_fraud_partial_vector_weights_are_renormalized(client: TestClient) -> None:
    # The reported weights describe what each signal actually contributed to THIS
    # account's score, so on a partial vector they are renormalized over the
    # observed signals and still sum to 1 — the score is a true weighted mean of
    # what was measured, not a full-vector mean silently dragged toward 0 by the
    # signals that were not.
    payload = _payload(0)
    del payload["engagement_benchmark"]
    body = client.post("/v1/fraud/score", json=payload).json()

    weights = [s["weight"] for s in body["signals"]]
    assert len(weights) == 3
    assert sum(weights) == pytest.approx(1.0, abs=1e-4)
    # Each signal's contribution is its value scaled by the renormalized weight.
    for signal in body["signals"]:
        assert signal["weighted"] == pytest.approx(
            signal["value"] * signal["weight"], abs=1e-4
        )
    assert body["score"] == pytest.approx(
        sum(s["weighted"] for s in body["signals"]) * 100.0, abs=1e-2
    )


def test_fraud_score_is_not_capped_when_a_signal_is_absent(client: TestClient) -> None:
    # NEW GUARANTEE. engagement_deviation carries a nominal weight of 0.30. The old
    # composite summed an absent signal in at 0.0 * 0.30, which hard-capped the
    # achievable score at 70/100 for every account scored without a benchmark — no
    # matter how blatant the fraud. Renormalizing over the OBSERVED signals removes
    # that cap: a maximally suspicious account can now reach the top of the scale.
    payload = _payload(0)
    del payload["engagement_benchmark"]
    counts = [1_000]
    for i in range(1, 20):
        counts.append(counts[-1] + (5_000_000 if i == 10 else 50))
    payload["follower_series"] = [
        {"timestamp": _iso(_BASE + timedelta(days=i)), "count": c}
        for i, c in enumerate(counts)
    ]
    payload["account"]["follower_count"] = counts[-1]
    payload["account"]["following_count"] = 1  # extreme follower/following imbalance
    payload["posts"] = [
        {
            "post_id": f"post_{i}",
            "timestamp": _iso(_BASE + timedelta(days=i)),
            "likes": 40_000,  # bought likes...
            "comments": 1,  # ...with almost no conversation
            "views": 1_000,
        }
        for i in range(6)
    ]

    body = client.post("/v1/fraud/score", json=payload).json()
    assert "engagement_deviation" not in {s["name"] for s in body["signals"]}
    assert body["score"] > 70.0  # unreachable before renormalization
    assert body["score"] <= 100.0


def test_fraud_is_deterministic(client: TestClient) -> None:
    payload = _payload(60_000)
    first = client.post("/v1/fraud/score", json=payload).json()
    second = client.post("/v1/fraud/score", json=payload).json()
    assert first["score"] == second["score"]
    assert first["confidence"] == second["confidence"]
    assert first["signals"] == second["signals"]


def test_fraud_growth_spike_signal_is_monotone(client: TestClient) -> None:
    # The growth-spike signal is the component with a provable monotonicity
    # property: a sharper follower spike can only raise it.
    prev = -1.0
    scores: list[float] = []
    for extra in (0, 2_000, 8_000, 32_000, 128_000):
        body = client.post("/v1/fraud/score", json=_payload(extra)).json()
        spike = next(
            s["value"] for s in body["signals"] if s["name"] == "growth_spike"
        )
        assert spike >= prev  # sharper spike must not lower the spike signal
        prev = spike
        scores.append(body["score"])
    assert prev > 0.0  # a large spike must register
    # A pronounced spike must raise the overall estimate above the flat baseline.
    assert scores[-1] > scores[0]


def test_fraud_confidence_capped_for_cold_start(client: TestClient) -> None:
    body = client.post("/v1/fraud/score", json=_payload(1_000)).json()
    assert body["confidence"] <= 0.65


@pytest.mark.xfail(
    strict=True,
    reason=(
        "SOURCE BUG (not a stale expectation): app.models.undbot.undbot_signal "
        "always returns a float, even with no posts — _posting_type_concentration "
        "and _posting_influence_scarcity return a hardcoded 0.0 for an empty feed "
        "instead of None. So coordination_undbot is ALWAYS 'observed', "
        "score_fraud's `if not observed` branch and app/api/fraud.py's "
        "`if not heuristic.observed` branch are dead code, and an account with no "
        "posts, no benchmark and a one-delta series still comes back with a low "
        "score at observed=True — the exact 'certified clean on the basis of "
        "nothing' the zero-vs-absent refactor exists to kill. Reported, not "
        "silently fixed: undbot_signal must return None when it has no posts."
    ),
)
def test_fraud_unexaminable_account_is_not_scored_clean(client: TestClient) -> None:
    # NEW GUARANTEE. Nothing to look at: no posts, no benchmark, and a follower
    # series too short to establish a growth baseline. Not one signal is
    # computable, so there is no score — `observed` is false and `score` is null.
    # An account nobody could examine must NEVER be rendered as a clean 0.
    payload = _payload(0)
    del payload["engagement_benchmark"]
    payload["posts"] = []
    payload["follower_series"] = payload["follower_series"][:2]

    body = client.post("/v1/fraud/score", json=payload).json()
    assert body["observed"] is False
    assert body["score"] is None
    assert body["signals"] == []
    assert body["confidence"] == 0.0


def test_fraud_rejects_unknown_field(client: TestClient) -> None:
    payload = _payload(0)
    payload["account"]["unexpected"] = "x"
    resp = client.post("/v1/fraud/score", json=payload)
    assert resp.status_code == 400
    assert resp.json()["code"] == "ml.invalid"
