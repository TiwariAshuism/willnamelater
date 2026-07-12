"""Register a challenger and promote a champion via the backend mlops endpoints.

DB-free and credential-free with respect to S3: the trainer never holds S3
keys. It POSTs the model bytes (base64) + manifest + validation report to the
backend, which writes S3 and records the registry row (§3, §5.2/§5.3). Promotion
is a separate call the backend re-validates server-side (defense in depth).

stdlib ``urllib`` only, mirroring ``labels.py`` — no runtime HTTP dependency.
"""

from __future__ import annotations

import base64
import hashlib
import json
import urllib.request

from training.artifact import MODEL_FILENAME


def snapshot_hash(audit_job_ids, features, targets) -> str:
    """sha256 over the ordered (audit_job_id, features, label) tuples actually
    used for training — the reproducibility marker the registry row stores. The
    same data in the same order yields the same hash; a changed fold changes it.
    """
    hasher = hashlib.sha256()
    for aid, feat, target in zip(audit_job_ids, features, targets, strict=True):
        # NaN is not JSON-stable; render it explicitly so a missing feature
        # hashes consistently across runs.
        cols = ["nan" if _is_nan(v) else repr(float(v)) for v in feat]
        hasher.update(f"{aid}|{','.join(cols)}|{target}\n".encode())
    return "sha256:" + hasher.hexdigest()


def _is_nan(v) -> bool:
    return isinstance(v, float) and v != v


def max_captured_at(captured_at) -> str:
    """The watermark: rows with captured_at <= this were eligible (§3)."""
    return max(captured_at) if captured_at else ""


def build_register_payload(
    *, model_name, manifest, model_bytes, metrics, validation_report,
    feature_snapshot, data_floor_counts,
) -> dict:
    """Assemble the POST /v1/admin/mlops/models body (§5.2)."""
    return {
        "model_name": model_name,
        "version": manifest["version"],
        "manifest": manifest,
        "model_file_name": MODEL_FILENAME,
        "model_file_b64": base64.b64encode(model_bytes).decode("ascii"),
        "metrics": metrics,
        "validation_report": validation_report,
        "feature_snapshot": feature_snapshot,
        "data_floor_counts": data_floor_counts,
    }


def _post_json(url: str, body: dict, *, token: str | None, timeout: int) -> dict:
    data = json.dumps(body).encode("utf-8")
    request = urllib.request.Request(url, data=data, method="POST")
    request.add_header("Content-Type", "application/json")
    request.add_header("Accept", "application/json")
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(request, timeout=timeout) as resp:  # noqa: S310
        raw = resp.read().decode("utf-8")
    return json.loads(raw) if raw else {}


def register_challenger(
    models_url: str, payload: dict, *, token: str | None = None, timeout: int = 60
) -> dict:
    """POST the challenger to /v1/admin/mlops/models. Idempotent on
    (model_name, version). Returns the registry row."""
    return _post_json(models_url, payload, token=token, timeout=timeout)


def promote(
    models_url: str, version: str, *, model_name: str, reason: str,
    override_shadow: bool = False, token: str | None = None, timeout: int = 60,
) -> dict:
    """POST /v1/admin/mlops/models/{version}/promote. Also used for rollback by
    passing an archived version. Returns the champion artifact to materialize."""
    url = f"{models_url.rstrip('/')}/{version}/promote"
    body = {"model_name": model_name, "reason": reason,
            "override_shadow": override_shadow}
    return _post_json(url, body, token=token, timeout=timeout)
