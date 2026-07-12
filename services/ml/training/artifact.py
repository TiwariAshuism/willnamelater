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


def artifact_version(model_bytes: bytes) -> str:
    """A deterministic version identifying the exact model. Same data + seed →
    same model bytes → same version, so a re-run is idempotent and a version
    pins the precise model that produced a score."""
    return "lgbm-" + hashlib.sha256(model_bytes).hexdigest()[:12]


def write_artifact(out_dir, model_bytes: bytes, *, metrics=None, counts=None):
    """Write the model file and the manifest.json the registry loads. Returns the
    manifest dict."""
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    version = artifact_version(model_bytes)
    (out / MODEL_FILENAME).write_bytes(model_bytes)
    manifest = {
        "version": version,
        "model_file": MODEL_FILENAME,
        "feature_order": list(FEATURE_ORDER),
        "metrics": metrics or {},
        "class_counts": counts or {},
    }
    (out / MANIFEST_FILENAME).write_text(
        json.dumps(manifest, indent=2, sort_keys=True), encoding="utf-8"
    )
    return manifest
