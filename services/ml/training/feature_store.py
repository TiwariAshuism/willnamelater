"""Read clean, labelled feature rows from the backend feature-store export.

DB-free by design (mirrors ``labels.py``): the ML service never speaks Postgres,
it reaches the backend over HTTP. This module fetches the frozen feature vectors
the Go ``mlops`` module captured once at audit time (eliminating train/serve
skew) and projects them onto the two independent model targets from the contract
(``RETRAINING_ARCHITECTURE.md`` §1):

- a supervised FRAUD classifier trains on the six frozen fraud keys plus the
  ``fraud_label`` backfilled on dispute decisions, and
- a REACH quantile regressor trains on the full numeric vector plus the
  ``reach_label`` pulled from OAuth Instagram Insights.

A model trains **only on rows that carry its label**. A missing feature is
``null`` in the export and becomes ``NaN`` here (LightGBM's native missing) — it
is never zero-filled, which would teach the model that "no signal" is a specific
point in feature space.
"""

from __future__ import annotations

import json
import math
import urllib.parse
import urllib.request
from dataclasses import dataclass

from training.features import FEATURE_ORDER

# The full numeric input vector for the REACH model: the six frozen fraud keys
# followed by the descriptive numeric sub-vector (§1.1). The high-cardinality
# string keys (``niche``, ``tier``, ``platform``) are deliberately NOT model
# inputs — they are carried separately as stratification dimensions (§4 G2), so
# the regressor never depends on an unstable one-hot of an open category set.
# ``verified`` is a bool encoded as 1.0 / 0.0 / NaN. The order is frozen for the
# same positional reason ``FEATURE_ORDER`` is.
REACH_FEATURE_ORDER = (
    *FEATURE_ORDER,
    "follower_count",
    "following_count",
    "follower_following_ratio",
    "engagement_rate",
    "engagement_rate_variance",
    "comment_like_ratio",
    "posting_cadence_per_week",
    "account_age_days_proxy",
    "post_count",
    "verified",
)


@dataclass(frozen=True)
class Stratum:
    """The cohort a held-out row belongs to, for per-tier / per-niche gating."""

    tier: str
    niche: str


@dataclass(frozen=True)
class FraudDataset:
    """Fraud rows: 6-column matrix, binary labels, temporal keys, strata.

    Every list is parallel and index-aligned. ``captured_at`` drives the
    temporal train/held-out split; ``strata`` drives G2.
    """

    features: list[list[float]]
    targets: list[int]
    captured_at: list[str]
    strata: list[Stratum]
    audit_job_ids: list[str]


@dataclass(frozen=True)
class ReachDataset:
    """Reach rows: full numeric matrix, integer reach labels, temporal keys."""

    features: list[list[float]]
    targets: list[float]
    captured_at: list[str]
    strata: list[Stratum]
    audit_job_ids: list[str]


def _num(value) -> float:
    """Project a JSON value onto a float, mapping ``null`` to ``NaN`` (native
    missing) and never to zero. Booleans map to 1.0 / 0.0."""
    if value is None:
        return math.nan
    if isinstance(value, bool):
        return 1.0 if value else 0.0
    return float(value)


def parse_export(payload) -> list[dict]:
    """Extract the rows list from a feature-row export dict."""
    return payload.get("rows") or []


def fetch_feature_rows(
    base_url: str,
    *,
    token: str | None = None,
    since: str | None = None,
    quality: str = "ok",
    limit: int = 5000,
    timeout: int = 60,
) -> list[dict]:
    """GET ``/v1/admin/mlops/feature-rows`` and return its rows list.

    ``base_url`` is the endpoint (e.g. ``http://localhost:8080/v1/admin/mlops/
    feature-rows``). ``quality='ok'`` (the default) asks the backend to exclude
    anti-gaming-rejected rows from the training fold; ``'all'`` includes them.
    """
    params: dict[str, str] = {"quality": quality, "limit": str(limit)}
    if since:
        params["since"] = since
    url = base_url + ("&" if "?" in base_url else "?") + urllib.parse.urlencode(params)
    request = urllib.request.Request(url)
    request.add_header("Accept", "application/json")
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(request, timeout=timeout) as resp:  # noqa: S310
        payload = json.loads(resp.read().decode("utf-8"))
    return parse_export(payload)


def fetch_canaries(
    base_url: str,
    *,
    model_name: str,
    token: str | None = None,
    active: bool = True,
    timeout: int = 30,
) -> list[dict]:
    """GET ``/v1/admin/mlops/canaries`` for a model and return its canaries list.

    An empty list is the honest cold-start reality (no verified accounts yet);
    the caller skips the canary gate with a recorded warning (§4 G3).
    """
    params = {"model_name": model_name, "active": "true" if active else "false"}
    url = base_url + ("&" if "?" in base_url else "?") + urllib.parse.urlencode(params)
    request = urllib.request.Request(url)
    request.add_header("Accept", "application/json")
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(request, timeout=timeout) as resp:  # noqa: S310
        payload = json.loads(resp.read().decode("utf-8"))
    return payload.get("canaries") or []


def _stratum(feats: dict) -> Stratum:
    niche = feats.get("niche")
    tier = feats.get("tier")
    return Stratum(
        tier=str(tier) if tier else "unknown",
        niche=str(niche) if niche else "unknown",
    )


def to_fraud_dataset(rows) -> FraudDataset:
    """Project export rows onto the fraud training matrix.

    Only rows that CARRY a fraud label are kept — a model trains solely on its
    own label. The six frozen fraud keys are always present in a captured
    vector; each is read verbatim from the stored jsonb (no recomputation).
    """
    features: list[list[float]] = []
    targets: list[int] = []
    captured_at: list[str] = []
    strata: list[Stratum] = []
    audit_job_ids: list[str] = []
    for row in rows:
        if row.get("fraud_label") is None:
            continue
        feats = row.get("features") or {}
        features.append([_num(feats.get(name)) for name in FEATURE_ORDER])
        targets.append(1 if row.get("fraud_label") else 0)
        captured_at.append(str(row.get("captured_at") or ""))
        strata.append(_stratum(feats))
        audit_job_ids.append(str(row.get("audit_job_id") or ""))
    return FraudDataset(features, targets, captured_at, strata, audit_job_ids)


def to_reach_dataset(rows) -> ReachDataset:
    """Project export rows onto the reach training matrix.

    Only rows that carry a real ``reach_label`` (from Instagram Insights) are
    kept. Missing features pass through as ``NaN`` — native LightGBM missing.
    """
    features: list[list[float]] = []
    targets: list[float] = []
    captured_at: list[str] = []
    strata: list[Stratum] = []
    audit_job_ids: list[str] = []
    for row in rows:
        if row.get("reach_label") is None:
            continue
        feats = row.get("features") or {}
        features.append([_num(feats.get(name)) for name in REACH_FEATURE_ORDER])
        targets.append(float(row["reach_label"]))
        captured_at.append(str(row.get("captured_at") or ""))
        strata.append(_stratum(feats))
        audit_job_ids.append(str(row.get("audit_job_id") or ""))
    return ReachDataset(features, targets, captured_at, strata, audit_job_ids)
