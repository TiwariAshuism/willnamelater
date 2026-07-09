"""Endpoint tests for /v1/pods/detect and /v1/comments/classify.

The pod test asserts a *structural* property of the clustering — a group of
commenters engineered to co-occur on the same posts is recoverable as a pod
while isolated commenters are not — plus determinism and bounded confidence.
No claim is made that any real account is fraudulent.
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


def _pod_payload() -> dict:
    events: list[dict] = []
    clique = ["u_a", "u_b", "u_c", "u_d"]
    # The clique comments together, within a minute, on six shared posts.
    for post in range(6):
        t0 = _BASE + timedelta(hours=post)
        for j, user in enumerate(clique):
            events.append(
                {
                    "post_id": f"shared_{post}",
                    "commenter": user,
                    "timestamp": _iso(t0 + timedelta(seconds=15 * j)),
                }
            )
    # Isolated commenters, each on their own post only.
    for k, user in enumerate(["lone_x", "lone_y", "lone_z"]):
        events.append(
            {
                "post_id": f"solo_{k}",
                "commenter": user,
                "timestamp": _iso(_BASE + timedelta(days=1, hours=k)),
            }
        )
    return {"events": events, "window_minutes": 60, "min_pod_size": 3}


def test_pods_schema_and_bounds(client: TestClient) -> None:
    resp = client.post("/v1/pods/detect", json=_pod_payload())
    assert resp.status_code == 200
    body = resp.json()

    assert body["estimate"] is True
    assert body["model_version"] == "heuristic"
    assert 0.0 <= body["confidence"] <= 1.0
    assert body["commenters_analyzed"] == 7
    for pod in body["pods"]:
        assert pod["size"] >= 3
        assert 0.0 <= pod["cohesion"] <= 1.0
        assert 0.0 <= pod["confidence"] <= 1.0


def test_pods_recovers_engineered_clique(client: TestClient) -> None:
    body = client.post("/v1/pods/detect", json=_pod_payload()).json()
    assert body["pods"], "an engineered co-occurring clique should form a pod"
    members = set(body["pods"][0]["members"])
    assert {"u_a", "u_b", "u_c", "u_d"}.issubset(members)
    assert not members & {"lone_x", "lone_y", "lone_z"}


def test_pods_is_deterministic(client: TestClient) -> None:
    payload = _pod_payload()
    first = client.post("/v1/pods/detect", json=payload).json()
    second = client.post("/v1/pods/detect", json=payload).json()
    assert first["pods"] == second["pods"]
    assert first["confidence"] == second["confidence"]


def test_pods_empty_input(client: TestClient) -> None:
    body = client.post(
        "/v1/pods/detect", json={"events": [], "window_minutes": 60, "min_pod_size": 3}
    ).json()
    assert body["pods"] == []
    assert body["commenters_analyzed"] == 0


def test_comments_classify_schema_and_bounds(client: TestClient) -> None:
    payload = {
        "comments": [
            {"id": "1", "text": "🔥🔥🔥"},
            {"id": "2", "text": "nice post"},
            {"id": "3", "text": "nice post"},
            {"id": "4", "text": "This is a thoughtful, specific reply about the topic"},
        ]
    }
    resp = client.post("/v1/comments/classify", json=payload)
    assert resp.status_code == 200
    body = resp.json()

    assert body["estimate"] is True
    assert body["model_version"] == "heuristic"
    assert 0.0 <= body["confidence"] <= 1.0
    assert 0.0 <= body["low_quality_ratio"] <= 1.0
    assert len(body["classifications"]) == 4
    for item in body["classifications"]:
        assert 0.0 <= item["confidence"] <= 1.0


def test_comments_classify_is_deterministic(client: TestClient) -> None:
    payload = {"comments": [{"id": "1", "text": "wow"}, {"id": "2", "text": "amazing"}]}
    first = client.post("/v1/comments/classify", json=payload).json()
    second = client.post("/v1/comments/classify", json=payload).json()
    # generated_at is a wall-clock stamp; everything else must match exactly.
    del first["generated_at"], second["generated_at"]
    assert first == second
