"""Write a trained model + manifest in the shape app.registry loads.

The registry (``app/registry/registry.py``) flips off the ``heuristic`` state
only when ``$INFLUAUDIT_ML_ARTIFACTS/manifest.json`` has string ``version`` and
``model_file`` keys and ``model_file`` resolves to a real file. This module
writes exactly that; the extra metadata keys are ignored by the registry but
keep the artifact auditable.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import Path

from training.features import FEATURE_ORDER

MODEL_FILENAME = "model.txt"
MANIFEST_FILENAME = "manifest.json"
# Subdirectory the challenger occupies during the shadow window (§3.2). The
# champion lives at the artifact-dir root; the registry's champion resolution is
# unchanged, the shadow slot is additive.
SHADOW_SUBDIR = "shadow"


def artifact_version(model_bytes: bytes) -> str:
    """A deterministic version identifying the exact model. Same data + seed →
    same model bytes → same version, so a re-run is idempotent and a version
    pins the precise model that produced a score."""
    return "lgbm-" + hashlib.sha256(model_bytes).hexdigest()[:12]


def build_manifest(model_bytes: bytes, *, feature_order=None, metrics=None,
                   counts=None, extra=None) -> dict:
    """The manifest.json the registry loads. ``feature_order`` defaults to the
    frozen fraud order for backward compatibility; the reach model passes its
    wider order. ``extra`` merges auditable metadata the registry ignores."""
    manifest = {
        "version": artifact_version(model_bytes),
        "model_file": MODEL_FILENAME,
        "feature_order": list(feature_order or FEATURE_ORDER),
        "metrics": metrics or {},
        "class_counts": counts or {},
    }
    if extra:
        manifest.update(extra)
    return manifest


def write_artifact(out_dir, model_bytes: bytes, *, metrics=None, counts=None,
                   feature_order=None, extra=None):
    """Write the model file and the manifest.json the registry loads. Returns the
    manifest dict."""
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    (out / MODEL_FILENAME).write_bytes(model_bytes)
    manifest = build_manifest(
        model_bytes, feature_order=feature_order, metrics=metrics,
        counts=counts, extra=extra,
    )
    (out / MANIFEST_FILENAME).write_text(
        json.dumps(manifest, indent=2, sort_keys=True), encoding="utf-8"
    )
    return manifest


def write_shadow_artifact(out_dir, model_bytes: bytes, **kwargs):
    """Write the challenger into ``<out_dir>/shadow/`` for the shadow window."""
    return write_artifact(Path(out_dir) / SHADOW_SUBDIR, model_bytes, **kwargs)


def clear_shadow_artifact(out_dir) -> None:
    """Remove the shadow slot (on promotion or challenger rejection). Idempotent."""
    shadow = Path(out_dir) / SHADOW_SUBDIR
    for name in (MANIFEST_FILENAME, MODEL_FILENAME):
        target = shadow / name
        if target.exists():
            target.unlink()
    if shadow.is_dir() and not any(shadow.iterdir()):
        shadow.rmdir()
