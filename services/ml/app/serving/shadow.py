"""Best-effort shadow prediction logging.

When a challenger artifact is present, the fraud endpoint scores it in parallel
with the champion, never shows it, and hands the pair here to be logged to the
backend prediction-ingest endpoint (RETRAINING_ARCHITECTURE §5.4). The log is
the durable record for the shadow champion-vs-challenger comparison and a second
copy of the model_version per score.

The call is:

* **best-effort** — any failure (network, auth, backend down, unconfigured env)
  is swallowed; it never affects the served response;
* **fire-and-forget** — emitted from a FastAPI background task, after the
  response is sent;
* **DB-free** — the ML service reaches the backend over HTTP with a service
  token, exactly like ``training/labels.py`` reaches the admin export. The
  stdlib client is imported lazily so the runtime stays lean.
"""

from __future__ import annotations

import hashlib
import json
import os
from dataclasses import dataclass

#: Backend base URL, e.g. ``http://backend:8080``. Absent → logging is a no-op.
BACKEND_BASE_URL_ENV = "INFLUAUDIT_BACKEND_BASE_URL"
#: Service bearer token for the ``/v1/ml`` group. Absent → logging is a no-op.
SERVICE_TOKEN_ENV = "INFLUAUDIT_ML_SERVICE_TOKEN"
#: Ingest route (service-token auth).
PREDICTIONS_PATH = "/v1/ml/predictions"


def features_hash(features: dict[str, float | None]) -> str:
    """A stable sha256 over the scored feature vector (per-score snapshot ref).

    Keys are sorted so the hash is independent of dict ordering; the value is the
    JSON the log stores as ``features_hash`` (§5.4).
    """
    canonical = json.dumps(features, sort_keys=True, separators=(",", ":"))
    digest = hashlib.sha256(canonical.encode("utf-8")).hexdigest()
    return f"sha256:{digest}"


@dataclass(frozen=True)
class ShadowRecord:
    """One champion-vs-challenger pair to log. Mirrors the §5.4 ingest body."""

    model_name: str
    champion_version: str
    champion_score: float
    challenger_version: str
    challenger_score: float
    features_hash: str
    scored_at: str
    audit_job_id: str | None = None

    def to_payload(self) -> dict:
        return {
            "model_name": self.model_name,
            "audit_job_id": self.audit_job_id,
            "champion_version": self.champion_version,
            "champion_score": self.champion_score,
            "challenger_version": self.challenger_version,
            "challenger_score": self.challenger_score,
            "features_hash": self.features_hash,
            "scored_at": self.scored_at,
        }


def emit(record: ShadowRecord) -> None:
    """POST the shadow record to the backend. Best-effort: never raises.

    When the backend URL or service token is not configured, this is a silent
    no-op — the challenger was still computed and (correctly) not shown; there is
    simply nowhere to durably log it yet.
    """
    base = os.environ.get(BACKEND_BASE_URL_ENV)
    token = os.environ.get(SERVICE_TOKEN_ENV)
    if not base or not token:
        return
    import urllib.error
    import urllib.request

    url = base.rstrip("/") + PREDICTIONS_PATH
    body = json.dumps(record.to_payload()).encode("utf-8")
    request = urllib.request.Request(url, data=body, method="POST")
    request.add_header("Content-Type", "application/json")
    request.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(request, timeout=5):
            return
    except (urllib.error.URLError, OSError, ValueError):
        # Shadow logging must never affect serving; drop it.
        return


def enqueue(background_tasks, record: ShadowRecord) -> None:
    """Schedule :func:`emit` as a background task (fire-and-forget)."""
    background_tasks.add_task(emit, record)
