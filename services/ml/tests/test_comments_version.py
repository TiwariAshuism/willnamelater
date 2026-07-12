"""The comment classifier must never borrow the FRAUD model's identity.

The defect these tests pin: ``/v1/comments/classify`` stamped ``model_version``
from the shared **fraud** registry. That registry reports the promoted fraud
champion's version. So the day a real LightGBM fraud model shipped, an 18-phrase
English regex would have started reporting a trained model's version into a
paying customer's PDF — a rule presenting itself as a model.

The primary guarantee, pinned by
:func:`test_comment_version_does_not_move_when_fraud_champion_is_promoted`: a live
fraud champion does not change one character of the comment endpoint's
``model_version``.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from app.main import create_app
from app.registry import (
    COMMENT_ARTIFACT_DIR_ENV,
    COMMENT_HEURISTIC_VERSION,
    CommentModelRegistry,
    get_comment_registry,
    get_registry,
)
from app.registry.registry import ARTIFACT_DIR_ENV, HEURISTIC_VERSION, ModelRegistry
from training.features import FEATURE_ORDER

_FRAUD_CHAMPION = "fraud-lgbm-2026-07-01"
_COMMENT_CHAMPION = "comments-distilbert-2027-01-01"

_PAYLOAD = {"comments": [{"id": "1", "text": "nice"}, {"id": "2", "text": "🔥"}]}


@pytest.fixture(scope="module")
def client() -> TestClient:
    return TestClient(create_app())


def _write_artifact(directory: Path, version: str) -> None:
    """A schema-shaped artifact fixture (manifest + stub file), not a real model."""
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "model.txt").write_text("stub-model-bytes", encoding="utf-8")
    (directory / "manifest.json").write_text(
        json.dumps(
            {
                "version": version,
                "model_file": "model.txt",
                "feature_order": list(FEATURE_ORDER),
            }
        ),
        encoding="utf-8",
    )


def test_cold_start_comment_version_is_the_hardcoded_heuristic(
    client: TestClient,
) -> None:
    body = client.post("/v1/comments/classify", json=_PAYLOAD).json()
    assert body["model_version"] == COMMENT_HEURISTIC_VERSION == "heuristic-comments-v1"
    # And NOT the fraud registry's cold-start string either: two estimators, two
    # namespaces, even when both happen to be in cold start.
    assert body["model_version"] != HEURISTIC_VERSION


def test_comment_version_does_not_move_when_fraud_champion_is_promoted(
    client: TestClient, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """THE regression test for the version leak.

    A fraud champion is genuinely live (asserted, so this fixture cannot rot into
    a no-op), and the comment endpoint's version is byte-identical to what it was
    in cold start.
    """
    before = client.post("/v1/comments/classify", json=_PAYLOAD).json()["model_version"]

    _write_artifact(tmp_path, _FRAUD_CHAMPION)
    monkeypatch.setattr(
        "app.registry.registry._default_registry", ModelRegistry(artifact_dir=tmp_path)
    )

    # The champion really is serving: without this the test would pass vacuously.
    assert get_registry().active_version() == _FRAUD_CHAMPION
    assert get_registry().is_supervised() is True

    after = client.post("/v1/comments/classify", json=_PAYLOAD).json()
    assert after["model_version"] == before == COMMENT_HEURISTIC_VERSION
    assert _FRAUD_CHAMPION not in after["model_version"]
    # The comment classifier is still rules: nothing about a fraud model makes it
    # supervised.
    assert get_comment_registry().is_supervised() is False


def test_comment_registry_ignores_the_fraud_artifact_env_var(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The fraud env var must not resolve a comment model, ever."""
    _write_artifact(tmp_path, _FRAUD_CHAMPION)
    monkeypatch.setenv(ARTIFACT_DIR_ENV, str(tmp_path))
    monkeypatch.delenv(COMMENT_ARTIFACT_DIR_ENV, raising=False)

    # The fraud registry sees it...
    assert ModelRegistry().active_version() == _FRAUD_CHAMPION
    # ...and the comment registry does not.
    registry = CommentModelRegistry()
    assert registry.active_version() == COMMENT_HEURISTIC_VERSION
    assert registry.active_ref() is None
    assert registry.is_supervised() is False


def test_comment_registry_resolves_its_own_artifact_when_one_exists(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The namespace is a real slot, not a constant: a *comment* artifact wins.

    This is what the honesty rule buys — the version string flips only when a real
    comment model is actually dropped in the comment directory.
    """
    _write_artifact(tmp_path, _COMMENT_CHAMPION)
    monkeypatch.setenv(COMMENT_ARTIFACT_DIR_ENV, str(tmp_path))

    registry = CommentModelRegistry()
    assert registry.active_version() == _COMMENT_CHAMPION
    assert registry.is_supervised() is True


def test_comment_registry_with_no_dir_is_heuristic() -> None:
    registry = CommentModelRegistry(artifact_dir=None)
    assert registry.active_version() == COMMENT_HEURISTIC_VERSION
    assert registry.is_supervised() is False


def test_health_still_reports_the_fraud_version(client: TestClient) -> None:
    """The comment namespace is additive: /health is the FRAUD probe, untouched."""
    body = client.get("/healthz").json()
    assert body["model_version"] == HEURISTIC_VERSION
