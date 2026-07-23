"""Registry champion + shadow-slot resolution.

Exercises the additive shadow slot with a temp artifact dir. No real model is
loaded — a manifest + a stub model file are enough to prove resolution mechanics
(the files are schema-shaped fixtures, not a trained model).
"""

from __future__ import annotations

import json
from pathlib import Path

from app.registry import ModelRegistry
from app.registry.registry import HEURISTIC_VERSION
from training.features import FEATURE_ORDER


def _write_artifact(directory: Path, version: str) -> None:
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


def test_cold_start_no_dir_is_heuristic() -> None:
    registry = ModelRegistry(artifact_dir=None)
    assert registry.active_version() == HEURISTIC_VERSION
    assert registry.active_ref() is None
    assert registry.is_supervised() is False
    assert registry.shadow_ref() is None
    assert registry.shadow_version() is None
    assert registry.has_shadow() is False


def test_empty_dir_stays_heuristic(tmp_path: Path) -> None:
    registry = ModelRegistry(artifact_dir=tmp_path)
    assert registry.active_version() == HEURISTIC_VERSION
    assert registry.has_shadow() is False


def test_champion_only_resolves_without_shadow(tmp_path: Path) -> None:
    _write_artifact(tmp_path, "lgbm-champion01")
    registry = ModelRegistry(artifact_dir=tmp_path)

    ref = registry.active_ref()
    assert ref is not None
    assert ref.version == "lgbm-champion01"
    assert ref.feature_order == FEATURE_ORDER
    assert registry.is_supervised() is True
    # No shadow sub-dir => champion path untouched, no challenger.
    assert registry.shadow_ref() is None
    assert registry.has_shadow() is False


def test_champion_and_shadow_resolve_independently(tmp_path: Path) -> None:
    _write_artifact(tmp_path, "lgbm-champion01")
    _write_artifact(tmp_path / "shadow", "lgbm-challenger1")
    registry = ModelRegistry(artifact_dir=tmp_path)

    assert registry.active_version() == "lgbm-champion01"
    assert registry.shadow_version() == "lgbm-challenger1"
    assert registry.has_shadow() is True
    # The champion is still the served version; the shadow never changes it.
    assert registry.active_ref().version == "lgbm-champion01"


def test_shadow_present_but_champion_cold_start(tmp_path: Path) -> None:
    # First-ever model: champion is still heuristic, challenger sits in shadow.
    _write_artifact(tmp_path / "shadow", "lgbm-challenger1")
    registry = ModelRegistry(artifact_dir=tmp_path)

    assert registry.active_version() == HEURISTIC_VERSION
    assert registry.is_supervised() is False
    assert registry.shadow_version() == "lgbm-challenger1"


def test_malformed_manifest_falls_back(tmp_path: Path) -> None:
    tmp_path.joinpath("manifest.json").write_text("{ not json", encoding="utf-8")
    registry = ModelRegistry(artifact_dir=tmp_path)
    assert registry.active_version() == HEURISTIC_VERSION


def test_manifest_without_model_file_does_not_resolve(tmp_path: Path) -> None:
    tmp_path.joinpath("manifest.json").write_text(
        json.dumps({"version": "lgbm-x", "model_file": "missing.txt"}),
        encoding="utf-8",
    )
    registry = ModelRegistry(artifact_dir=tmp_path)
    assert registry.active_ref() is None
