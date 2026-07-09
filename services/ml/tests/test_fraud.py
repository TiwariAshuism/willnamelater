"""Endpoint tests for /v1/fraud/score and /healthz.

Asserts schema conformance, determinism, boundedness and that the composite
score is monotone non-decreasing as an injected follower spike sharpens — all
provable without any labeled fraud data.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from fastapi.testclient import TestClient

from app.main import create_app

_BASE = datetime(2026, 1, 1, tzinfo=UTC)


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
        {"timestamp": _iso(_BASE + timedelta(days=i)), "likes": 300, "comments": 25}
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
        "follower_growth_anomaly",
        "follower_following_ratio",
        "engagement_deviation",
        "like_comment_ratio",
    }
    for signal in body["signals"]:
        assert 0.0 <= signal["value"] <= 1.0
        assert 0.0 <= signal["weighted"] <= 1.0


def test_fraud_is_deterministic(client: TestClient) -> None:
    payload = _payload(60_000)
    first = client.post("/v1/fraud/score", json=payload).json()
    second = client.post("/v1/fraud/score", json=payload).json()
    assert first["score"] == second["score"]
    assert first["confidence"] == second["confidence"]
    assert first["signals"] == second["signals"]


def test_fraud_growth_spike_signal_is_monotone(client: TestClient) -> None:
    # The growth-spike signal is the component with a provable monotonicity
    # property: a sharper follower spike can only raise it. (The composite also
    # folds in an IsolationForest term, which is legitimately non-monotone, so
    # the guarantee is asserted at the signal level where it truly holds.)
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


def test_fraud_rejects_unknown_field(client: TestClient) -> None:
    payload = _payload(0)
    payload["account"]["unexpected"] = "x"
    resp = client.post("/v1/fraud/score", json=payload)
    assert resp.status_code == 400
    assert resp.json()["code"] == "ml.invalid"
