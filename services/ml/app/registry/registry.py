"""Versioned model loading.

This is built for the supervised model (a LightGBM fraud classifier) that will
exist *once the dispute queue has produced enough labels*. Until then there is
no artifact to load, and the registry reports the honest cold-start state:
``heuristic``.

Deliberately, no model file ships with the service. Shipping a placeholder
``.lgb`` would let the rest of the system believe a trained model exists and
silently trust its output. The registry instead looks for a real artifact in a
configurable directory and, finding none, falls back to the rule-based path.
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass
from pathlib import Path

#: Active version reported while no trained artifact is present. This is a
#: truthful state, not a stub: the service really is running on heuristics.
HEURISTIC_VERSION = "heuristic"

#: Environment variable pointing at a directory of trained artifacts.
ARTIFACT_DIR_ENV = "INFLUAUDIT_ML_ARTIFACTS"

#: Manifest a future training job must drop next to its model file. Its
#: presence — and only its presence — flips the registry off the heuristic
#: path.
MANIFEST_NAME = "manifest.json"


@dataclass(frozen=True)
class ArtifactRef:
    """A resolved, on-disk supervised model artifact."""

    version: str
    model_path: Path


class ModelRegistry:
    """Resolves the active scoring model from the artifact directory.

    The registry never fabricates an artifact. ``active_version`` returns the
    manifest's version when a valid artifact is present and ``HEURISTIC_VERSION``
    otherwise.
    """

    def __init__(self, artifact_dir: Path | None = None) -> None:
        if artifact_dir is None:
            env = os.environ.get(ARTIFACT_DIR_ENV)
            artifact_dir = Path(env) if env else None
        self._artifact_dir = artifact_dir

    def _resolve(self) -> ArtifactRef | None:
        if self._artifact_dir is None:
            return None
        manifest = self._artifact_dir / MANIFEST_NAME
        if not manifest.is_file():
            return None
        try:
            meta = json.loads(manifest.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return None
        version = meta.get("version")
        model_file = meta.get("model_file")
        if not isinstance(version, str) or not isinstance(model_file, str):
            return None
        model_path = self._artifact_dir / model_file
        if not model_path.is_file():
            return None
        return ArtifactRef(version=version, model_path=model_path)

    def active_version(self) -> str:
        """Return the active model version, or ``heuristic`` in cold start."""
        ref = self._resolve()
        return ref.version if ref is not None else HEURISTIC_VERSION

    def is_supervised(self) -> bool:
        """True only once a real trained artifact has been resolved."""
        return self._resolve() is not None


_default_registry = ModelRegistry()


def get_registry() -> ModelRegistry:
    """Return the process-wide registry.

    A single instance is enough: artifact resolution is cheap and reflects the
    filesystem at call time, so a newly deployed model is picked up without a
    restart.
    """
    return _default_registry
