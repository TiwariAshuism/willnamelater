"""Endpoint tests for /v1/pods/detect and /v1/comments/classify.

The pod tests assert *structural* properties of the co-commenter clique model —
an engineered group that co-comments on shared posts forms a maximal clique
while isolated commenters do not, the count is deterministic, and injecting more
coordinated structure never lowers the clique count (the label-free monotonicity
property, mirroring ``features/follower.py``). No claim is made that any real
account is fraudulent.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from fastapi.testclient import TestClient

from app.main import create_app

_BASE = datetime(2026, 1, 1, tzinfo=UTC)

# A coordinated group co-comments on this many shared posts; >= min_shared_posts
# so every within-group pair is an edge and the group is a maximal clique.
_SHARED_POSTS = 3
_GROUP_SIZE = 5


@pytest.fixture(scope="module")
def client() -> TestClient:
    return TestClient(create_app())


def _iso(dt: datetime) -> str:
    return dt.isoformat()


def _clique_payload(n_groups: int, n_lone: int = 0) -> dict:
    events: list[dict] = []
    t = _BASE
    for g in range(n_groups):
        members = [f"g{g}_u{m}" for m in range(_GROUP_SIZE)]
        for post in range(_SHARED_POSTS):
            for member in members:
                t += timedelta(seconds=1)
                events.append(
                    {
                        "post_id": f"g{g}_shared_{post}",
                        "commenter": member,
                        "text": "great video",
                        "timestamp": _iso(t),
                    }
                )
    # Isolated commenters, each on their own post only.
    for k in range(n_lone):
        t += timedelta(seconds=1)
        events.append(
            {
                "post_id": f"solo_{k}",
                "commenter": f"lone_{k}",
                "text": "unique thought",
                "timestamp": _iso(t),
            }
        )
    return {"events": events, "min_pod_size": 5, "min_shared_posts": 2}


def test_pods_schema_and_bounds(client: TestClient) -> None:
    resp = client.post("/v1/pods/detect", json=_clique_payload(2, n_lone=3))
    assert resp.status_code == 200
    body = resp.json()

    assert body["estimate"] is True
    assert body["model_version"] == "heuristic"
    assert body["partial"] is False
    assert 0.0 <= body["confidence"] <= 1.0
    assert body["commenters_analyzed"] == 2 * _GROUP_SIZE + 3
    assert body["clique_count"] >= 2
    assert 0.0 <= body["clique_membership_fraction"] <= 1.0

    names = {s["name"] for s in body["signals"]}
    assert names == {"maximal_clique_density", "clique_membership_fraction"}
    for signal in body["signals"]:
        assert 0.0 <= signal["value"] <= 1.0
        assert 0.0 <= signal["weighted"] <= 1.0

    for pod in body["pods"]:
        assert pod["size"] >= 5
        assert 0.0 <= pod["cohesion"] <= 1.0
        assert 0.0 <= pod["confidence"] <= 1.0


def test_pods_recovers_engineered_clique(client: TestClient) -> None:
    body = client.post("/v1/pods/detect", json=_clique_payload(1, n_lone=3)).json()
    assert body["pods"], "an engineered co-commenting group should form a clique"
    members = set(body["pods"][0]["members"])
    assert {f"g0_u{m}" for m in range(_GROUP_SIZE)}.issubset(members)
    assert not members & {"lone_0", "lone_1", "lone_2"}
    # Five clique members out of eight commenters.
    assert body["clique_membership_fraction"] == pytest.approx(5 / 8)


def test_pods_clique_count_is_monotone(client: TestClient) -> None:
    # Injecting more disjoint coordinated groups must never lower the clique
    # count — the label-free property this model is asserted against.
    prev = -1
    for n_groups in (1, 2, 3, 4):
        body = client.post(
            "/v1/pods/detect", json=_clique_payload(n_groups)
        ).json()
        count = body["clique_count"]
        assert count >= prev
        prev = count
    assert prev >= 4  # four disjoint groups yield at least four cliques


def test_pods_is_deterministic(client: TestClient) -> None:
    payload = _clique_payload(2, n_lone=2)
    first = client.post("/v1/pods/detect", json=payload).json()
    second = client.post("/v1/pods/detect", json=payload).json()
    assert first["clique_count"] == second["clique_count"]
    assert first["pods"] == second["pods"]
    assert first["confidence"] == second["confidence"]


def test_pods_empty_input(client: TestClient) -> None:
    body = client.post(
        "/v1/pods/detect", json={"events": [], "min_pod_size": 5, "min_shared_posts": 2}
    ).json()
    assert body["pods"] == []
    assert body["clique_count"] == 0
    assert body["clique_membership_fraction"] == 0.0
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
