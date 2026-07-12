"""Versioned model loading.

This is built for the supervised model (a LightGBM fraud classifier) that will
exist *once the dispute queue has produced enough labels*. Until then there is
no artifact to load, and the registry reports the honest cold-start state:
``heuristic``.

Deliberately, no model file ships with the service. Shipping a placeholder
``.lgb`` would let the rest of the system believe a trained model exists and
silently trust its output. The registry instead looks for a real artifact in a
configurable directory and, finding none, falls back to the rule-based path.

Two slots resolve from the same directory:

* the **champion** — ``$INFLUAUDIT_ML_ARTIFACTS/manifest.json`` — serves users.
  Its resolution is unchanged from the original contract.
* the optional **challenger** — ``$INFLUAUDIT_ML_ARTIFACTS/shadow/manifest.json``
  — is scored in parallel during a shadow window (never shown to a user) so a
  candidate model can be compared against the champion on live traffic. The
  shadow slot is additive: when it is absent the champion path is untouched and
  cold start still reports ``heuristic``.
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

#: Sub-directory of the artifact dir holding the challenger during a shadow
#: window (RETRAINING_ARCHITECTURE §3.2). Cleared on promotion/rollback.
SHADOW_DIR_NAME = "shadow"


@dataclass(frozen=True)
class ArtifactRef:
    """A resolved, on-disk supervised model artifact.

    ``feature_order`` is the positional input contract read from the manifest.
    It is optional so an older manifest without the key still resolves; the
    supervised scorer requires it and treats its absence as unservable.
    """

    version: str
    model_path: Path
    feature_order: tuple[str, ...] | None = None


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

    def _resolve_dir(self, directory: Path | None) -> ArtifactRef | None:
        """Resolve a manifest+model pair in ``directory`` or return ``None``.

        Shared by the champion and challenger slots so both apply the exact same
        validation: a real manifest with string ``version`` / ``model_file`` and
        a model file that actually exists on disk.
        """
        if directory is None:
            return None
        manifest = directory / MANIFEST_NAME
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
        model_path = directory / model_file
        if not model_path.is_file():
            return None
        raw_order = meta.get("feature_order")
        feature_order: tuple[str, ...] | None = None
        if isinstance(raw_order, list) and all(isinstance(x, str) for x in raw_order):
            feature_order = tuple(raw_order)
        return ArtifactRef(
            version=version, model_path=model_path, feature_order=feature_order
        )

    def _resolve(self) -> ArtifactRef | None:
        return self._resolve_dir(self._artifact_dir)

    def _shadow_dir(self) -> Path | None:
        if self._artifact_dir is None:
            return None
        return self._artifact_dir / SHADOW_DIR_NAME

    def active_version(self) -> str:
        """Return the active model version, or ``heuristic`` in cold start."""
        ref = self._resolve()
        return ref.version if ref is not None else HEURISTIC_VERSION

    def active_ref(self) -> ArtifactRef | None:
        """The resolved champion artifact, or ``None`` in cold start."""
        return self._resolve()

    def is_supervised(self) -> bool:
        """True only once a real trained artifact has been resolved."""
        return self._resolve() is not None

    def shadow_ref(self) -> ArtifactRef | None:
        """The resolved challenger artifact, or ``None`` when no shadow is set."""
        return self._resolve_dir(self._shadow_dir())

    def shadow_version(self) -> str | None:
        """The challenger version during a shadow window, else ``None``."""
        ref = self.shadow_ref()
        return ref.version if ref is not None else None

    def has_shadow(self) -> bool:
        """True only when a valid challenger artifact is present."""
        return self.shadow_ref() is not None


_default_registry = ModelRegistry()


def get_registry() -> ModelRegistry:
    """Return the process-wide registry.

    A single instance is enough: artifact resolution is cheap and reflects the
    filesystem at call time, so a newly deployed model is picked up without a
    restart.
    """
    return _default_registry
